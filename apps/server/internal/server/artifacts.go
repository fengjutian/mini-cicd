package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) artifactManifest(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentID(w, r)
	if !ok {
		return
	}
	root, err := s.artifactRoot(r, id)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	items := []map[string]any{}
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("artifact contains a symbolic link")
		}
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		rel, _ := filepath.Rel(root, name)
		items = append(items, map[string]any{"path": filepath.ToSlash(rel), "size": info.Size(), "sha256": hex.EncodeToString(h.Sum(nil))})
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "artifact_manifest_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deploymentId": id, "items": items})
}

func (s *Server) downloadArtifact(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentID(w, r)
	if !ok {
		return
	}
	root, err := s.artifactRoot(r, id)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	name := filepath.Clean(filepath.FromSlash(r.PathValue("path")))
	target := filepath.Join(root, name)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		writeError(w, http.StatusBadRequest, "invalid_artifact_path", "Artifact path is invalid.")
		return
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		writeError(w, http.StatusNotFound, "artifact_not_found", "Artifact file was not found.")
		return
	}
	if contentType := mime.TypeByExtension(filepath.Ext(target)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(filepath.Base(target), `"`, "")+`"`)
	http.ServeFile(w, r, target)
}

func (s *Server) artifactRoot(r *http.Request, id int64) (string, error) {
	var root string
	err := s.db.QueryRowContext(r.Context(), `SELECT COALESCE(artifact_path,'') FROM deployments WHERE id=? AND status='succeeded'`, id).Scan(&root)
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", errors.New("deployment has no saved artifacts")
	}
	return filepath.Abs(root)
}
