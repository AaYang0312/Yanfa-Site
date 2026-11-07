package httpapi

import (
	"errors"
	"io"
	"net/http"
	"time"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/media"
)

func RegisterMedia(mux *http.ServeMux, m AuthMiddleware, service *media.Service) {
	mux.Handle("GET /api/admin/media", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		q, err := DecodeQuery[contract.MediaQuery](r, "admin.schema.json#/$defs/MediaQuery")
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := service.List(r.Context(), s.Actor, q)
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 200, out)
	}))
	mux.Handle("POST /api/admin/media", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		r.Body = http.MaxBytesReader(w, r.Body, media.MaxBytes+64*1024)
		reader, err := r.MultipartReader()
		if err != nil {
			WriteError(w, contract.NewBusinessError(400, "INVALID_MULTIPART", "需要单文件 multipart/form-data 请求。"))
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			WriteError(w, multipartError(err))
			return
		}
		defer part.Close()
		if part.FormName() != "file" || part.FileName() == "" {
			WriteError(w, contract.NewBusinessError(422, "INVALID_MULTIPART", "仅允许一个 file 图片字段。", "file"))
			return
		}
		prepared, err := service.Prepare(r.Context(), part, part.FileName())
		if err != nil {
			WriteError(w, multipartError(err))
			return
		}
		defer prepared.Close()
		if next, e := reader.NextPart(); e != io.EOF {
			if next != nil {
				next.Close()
			}
			if e != nil {
				WriteError(w, multipartError(e))
			} else {
				WriteError(w, contract.NewBusinessError(422, "INVALID_MULTIPART", "仅允许一个 file 图片字段。", "file"))
			}
			return
		}
		out, err := service.UploadPrepared(r.Context(), s.Actor, prepared)
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 201, out)
	}))
	mux.Handle("GET /api/admin/media/{id}/file", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		file, record, err := service.File(r.Context(), s.Actor, r.PathValue("id"))
		if err != nil {
			WriteError(w, err)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "image/"+record.Format)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", "inline")
		uploaded, _ := time.Parse(time.RFC3339Nano, record.UploadedAt)
		http.ServeContent(w, r, record.MediaID, uploaded, file)
	}))
	mux.Handle("DELETE /api/admin/media/{id}", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		if err := service.Delete(r.Context(), s.Actor, r.PathValue("id")); err != nil {
			WriteError(w, err)
			return
		}
		w.WriteHeader(204)
	}))
}
func multipartError(err error) error {
	var business *contract.BusinessError
	if errors.As(err, &business) {
		return err
	}
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		return contract.NewBusinessError(413, "IMAGE_TOO_LARGE", "上传请求超过上限。", "file")
	}
	return contract.NewBusinessError(400, "INVALID_MULTIPART", "无法读取完整图片上传。", "file")
}
