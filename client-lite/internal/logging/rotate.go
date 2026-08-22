package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	DefaultMaxSize = int64(10 * 1024 * 1024)
	DefaultBackups = 3
	// MaximumLogFootprint is the hard upper bound for one current log and all
	// of its retained backups.
	MaximumLogFootprint = int64(50 * 1024 * 1024)
)

type RotatingFile struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	backups int
	file    *os.File
	size    int64
}

func NewRotatingFile(dir, name string, maxSize int64, backups int) (*RotatingFile, error) {
	if maxSize <= 0 || backups < 1 {
		return nil, fmt.Errorf("无效的日志轮转参数")
	}
	maxFiles := int(MaximumLogFootprint / maxSize)
	if maxSize > MaximumLogFootprint || maxFiles < 2 || backups > maxFiles-1 {
		return nil, fmt.Errorf("日志及其备份总大小不能超过 50 MiB")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	writer := &RotatingFile{path: filepath.Join(dir, name), maxSize: maxSize, backups: backups}
	if err := writer.pruneBackups(); err != nil {
		return nil, err
	}
	if err := writer.open(); err != nil {
		return nil, err
	}
	if writer.size > writer.maxSize {
		if err := writer.file.Close(); err != nil {
			return nil, err
		}
		writer.file = nil
		if err := os.Remove(writer.path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err := writer.open(); err != nil {
			return nil, err
		}
	} else if writer.size == writer.maxSize {
		if err := writer.rotate(); err != nil {
			_ = writer.file.Close()
			return nil, err
		}
	}
	return writer, nil
}

func (w *RotatingFile) pruneBackups() error {
	dir := filepath.Dir(w.path)
	prefix := filepath.Base(w.path) + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), prefix))
		if err != nil || index < 1 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if index > w.backups || info.Size() > w.maxSize {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (w *RotatingFile) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if int64(len(data)) <= w.maxSize && w.size > 0 && w.size+int64(len(data)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	written := 0
	for len(data) > 0 {
		if w.size >= w.maxSize {
			if err := w.rotate(); err != nil {
				return written, err
			}
		}
		remaining := w.maxSize - w.size
		chunkSize := len(data)
		if int64(chunkSize) > remaining {
			chunkSize = int(remaining)
		}
		n, err := w.file.Write(data[:chunkSize])
		w.size += int64(n)
		written += n
		data = data[n:]
		if err != nil {
			return written, err
		}
		if n != chunkSize {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func (w *RotatingFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingFile) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	return nil
}

func (w *RotatingFile) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}
	oldest := fmt.Sprintf("%s.%d", w.path, w.backups)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := w.backups - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", w.path, i)
		to := fmt.Sprintf("%s.%d", w.path, i+1)
		if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return w.open()
}
