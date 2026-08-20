package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type blobStore struct {
	root string
}

func newBlobStore(root string) (*blobStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("blob root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve blob root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create blob root: %w", err)
	}
	return &blobStore{root: abs}, nil
}

func (s *blobStore) Put(id string, source io.Reader, maxBytes int64) (path string, size int64, digest string, err error) {
	if s == nil || !validLocalResourceID(id) {
		return "", 0, "", errors.New("invalid blob identifier")
	}
	if maxBytes <= 0 {
		return "", 0, "", errors.New("invalid blob size limit")
	}
	temporary, err := os.CreateTemp(s.root, ".upload-*")
	if err != nil {
		return "", 0, "", fmt.Errorf("create blob temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", 0, "", fmt.Errorf("secure blob temporary file: %w", err)
	}
	hash := sha256.New()
	limited := &io.LimitedReader{R: source, N: maxBytes + 1}
	size, err = io.Copy(io.MultiWriter(temporary, hash), limited)
	if err != nil {
		return "", 0, "", fmt.Errorf("write blob: %w", err)
	}
	if size == 0 {
		return "", 0, "", errors.New("uploaded file is empty")
	}
	if size > maxBytes {
		return "", 0, "", fmt.Errorf("uploaded file exceeds %d byte limit", maxBytes)
	}
	if err := temporary.Sync(); err != nil {
		return "", 0, "", fmt.Errorf("sync blob: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", 0, "", fmt.Errorf("close blob: %w", err)
	}
	finalPath := filepath.Join(s.root, id+".blob")
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return "", 0, "", fmt.Errorf("commit blob: %w", err)
	}
	committed = true
	return finalPath, size, hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *blobStore) Open(path string) (*os.File, error) {
	resolved, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("blob path is not a regular file")
	}
	return os.Open(resolved)
}

func (s *blobStore) Read(path string, maxBytes int64) ([]byte, error) {
	file, err := s.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("blob exceeds %d byte expansion limit", maxBytes)
	}
	return data, nil
}

func (s *blobStore) Delete(path string) error {
	resolved, err := s.resolve(path)
	if err != nil {
		return err
	}
	if err := os.Remove(resolved); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete blob: %w", err)
	}
	return nil
}

func (s *blobStore) resolve(path string) (string, error) {
	if s == nil {
		return "", errors.New("blob store is unavailable")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(s.root, abs)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." || filepath.IsAbs(relative) {
		return "", errors.New("blob path escapes the configured data root")
	}
	return abs, nil
}

func validLocalResourceID(id string) bool {
	if id == "" {
		return false
	}
	for _, char := range id {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}
