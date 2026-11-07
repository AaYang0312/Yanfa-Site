package publication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"yuanfa-official/backend/internal/release"
)

// CodeVersion identifies the actual executable without requiring .git in a
// deployment template or trusting a request-supplied version string.
func CodeVersion() (string, error) {
	name, err := os.Executable()
	if err != nil {
		return "", err
	}
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return "binary_sha256_" + hex.EncodeToString(h.Sum(nil)), nil
}
func (s *Service) buildLoop() {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if s.ctx.Err() != nil {
			return
		}
		var id, frozen, code string
		err := s.db.QueryRowContext(s.ctx, `UPDATE candidates SET state='building',started_at=? WHERE id=(SELECT id FROM candidates WHERE state='queued' ORDER BY created_at,id LIMIT 1) RETURNING id,snapshot_json,code_version`, now()).Scan(&id, &frozen, &code)
		if err == nil {
			s.build(id, []byte(frozen), code)
			continue
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-tick.C:
		}
	}
}

// killTree handles the detached npm process group used by the integrated T05
// copier as well as Node's descendants. Capture children before killing parents.
func killTree(pid int) {
	b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/task/" + strconv.Itoa(pid) + "/children")
	for _, child := range strings.Fields(string(b)) {
		n, err := strconv.Atoi(child)
		if err == nil {
			killTree(n)
		}
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
func (s *Service) build(id string, frozen []byte, code string) {
	err := s.buildArtifact(id, frozen, code)
	if err != nil {
		failure := detail("BUILD_FAILED", "候选构建或审计失败，线上版本未变化。")
		if errors.Is(err, context.DeadlineExceeded) {
			failure = detail("BUILD_TIMEOUT", "候选构建超过时限，构建进程已终止，线上版本未变化。")
		} else if errors.Is(err, context.Canceled) {
			failure = detail("INTERRUPTED", "候选构建中断，请重新创建候选。")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.db.ExecContext(ctx, `UPDATE candidates SET state='failed',completed_at=?,error_json=? WHERE id=? AND state='building'`, now(), encode(failure), id)
	}
}
func (s *Service) buildArtifact(id string, frozen []byte, code string) error {
	for _, directory := range []string{s.dataPath("work"), s.dataPath("artifacts")} {
		if err := ensurePrivate(directory); err != nil {
			return err
		}
	}
	private := s.dataPath("work", id)
	if err := os.Mkdir(private, 0700); err != nil {
		return err
	}
	for _, name := range []string{"tmp", "cache"} {
		if err := os.Mkdir(filepath.Join(private, name), 0700); err != nil {
			return err
		}
	}
	snapshotPath := filepath.Join(private, "catalog.json")
	if err := os.WriteFile(snapshotPath, frozen, 0600); err != nil {
		return err
	}
	staging := filepath.Join(private, "artifact")
	if err := os.Mkdir(staging, 0700); err != nil {
		return err
	}
	timeout := s.cfg.BuildTimeout
	if timeout <= 0 || timeout > 300*time.Second {
		timeout = 300 * time.Second
	}
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()
	heap := s.cfg.BuildNodeHeapMB
	if heap <= 0 {
		heap = 512
	}
	// Linux resource limits are applied only to this child. No shell or user text
	// enters argv. Node itself receives a bounded heap; descendants inherit limits.
	args := []string{"--core=0", "--nofile=1024", "--fsize=1073741824", "--cpu=300:301", "--", "node", "--max-old-space-size=" + strconv.Itoa(heap), filepath.Join(s.cfg.TemplateDir, "scripts", "build-candidate.mjs"), "--template", s.cfg.TemplateDir, "--work", filepath.Join(private, "build"), "--snapshot", snapshotPath, "--media-root", s.dataPath("media"), "--out", filepath.Join(staging, "site"), "--site-url", s.cfg.SiteURL}
	cmd := exec.Command("prlimit", args...)
	cmd.Dir = s.cfg.TemplateDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	env := []string{}
	// Build receives no admin/session/model credentials. Product text remains JSON.
	for _, key := range []string{"PATH", "LANG", "LC_ALL", "TZ", "HOME"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	cmd.Env = append(env, "CODE_VERSION="+code, "TMPDIR="+filepath.Join(private, "tmp"), "npm_config_cache="+filepath.Join(private, "cache"), "ASTRO_TELEMETRY_DISABLED=1", "NODE_OPTIONS=--max-old-space-size="+strconv.Itoa(heap))
	log, err := os.OpenFile(filepath.Join(private, "build.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		killTree(cmd.Process.Pid)
		<-done
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	manifest, err := os.ReadFile(filepath.Join(private, "build", "manifest.json"))
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(staging, "catalog.json"), frozen, 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(staging, "manifest.json"), manifest, 0600); err != nil {
		return err
	}
	destination := s.dataPath("artifacts", id)
	if err = release.Seal(staging, destination); err != nil {
		return err
	}
	h, err := release.Load(s.cfg.DataDir, id)
	if err != nil {
		return err
	}
	if h.SHA256() != release.Hash(frozen) || h.CodeVersion() != code {
		return errors.New("candidate identity mismatch")
	}
	// A crash between sealing and ready leaves a private, unauthorized artifact.
	_, err = s.db.ExecContext(s.ctx, `UPDATE candidates SET state='ready',artifact_path=?,manifest_sha256=?,validation_json=json_set(validation_json,'$.build','passed','$.audit','passed','$.resources','verified'),completed_at=?,error_json=NULL WHERE id=? AND state='building'`, filepath.Join("artifacts", id), h.ManifestSHA256(), now(), id)
	return err
}
