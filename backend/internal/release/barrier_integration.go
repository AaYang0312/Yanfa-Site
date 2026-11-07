//go:build integration

package release

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Barrier uses only the private integration control directory, never HTTP.
// scenario.json may be replaced between activations by the owning harness.
func Barrier(ctx context.Context, name string) error {
	directory := os.Getenv("YUANFA_INTEGRATION_DIR")
	if directory == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(directory, "scenario.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var scenario struct {
		Barrier string `json:"barrier"`
	}
	if err = json.Unmarshal(b, &scenario); err != nil {
		return err
	}
	if scenario.Barrier != name {
		return nil
	}
	reached, err := json.Marshal(map[string]any{"barrier": name, "pid": os.Getpid()})
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(directory, "reached.json"), reached, 0600); err != nil {
		return err
	}
	timer := time.NewTicker(20 * time.Millisecond)
	defer timer.Stop()
	for {
		if _, err = os.Stat(filepath.Join(directory, "continue")); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
