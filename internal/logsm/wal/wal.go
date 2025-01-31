package wal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/internal/bufferpool"
	"github.com/tochemey/goakt/v2/internal/internalpb"
)

var errNilFD = errors.New("file must not be nil")

type WAL struct {
	mu      sync.Mutex
	file    *os.File
	dir     string
	path    string
	version string
}

func Create(dir string) (*WAL, error) {
	createdAt := time.Now()
	version := fmt.Sprintf("%s-%d", createdAt.Format("20060102150405"), createdAt.Nanosecond())

	name := path.Join(dir, fmt.Sprintf("wal-%s.log", version))

	file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	return &WAL{
		file:    file,
		dir:     dir,
		path:    name,
		version: version,
	}, nil
}

func Open(file string) (*WAL, error) {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return nil, err
	}

	fd, err := os.OpenFile(file, os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}

	return &WAL{
		file:    fd,
		dir:     filepath.Dir(file),
		path:    file,
		version: ParseVersion(path.Base(file)),
	}, nil
}

func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.close()
}

func (w *WAL) Reset() (*WAL, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.close(); err != nil {
		return nil, err
	}
	return Create(w.dir)
}

func (w *WAL) Delete() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.close(); err != nil {
		return err
	}
	if err := os.Remove(w.path); err != nil {
		return err
	}
	return nil
}

func (w *WAL) Write(entries ...*internalpb.Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return errNilFD
	}

	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	buf := bufferpool.Pool.Get()
	defer bufferpool.Pool.Put(buf)

	for _, entry := range entries {
		data, err := proto.Marshal(entry)
		if err != nil {
			return err
		}

		// data length
		n := int64(len(data))
		err = binary.Write(buf, binary.LittleEndian, n)
		if err != nil {
			return err
		}
		// data body
		err = binary.Write(buf, binary.LittleEndian, data)
		if err != nil {
			return err
		}
	}

	err := binary.Write(w.file, binary.LittleEndian, buf.Bytes())
	if err != nil {
		return err
	}

	return nil
}

func (w *WAL) Read() ([]*internalpb.Entry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil, errNilFD
	}

	// if file empty
	info, err := os.Stat(w.path)
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 {
		return nil, nil
	}

	// reset cursor
	if _, err = w.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	buf := bufferpool.Pool.Get()
	defer bufferpool.Pool.Put(buf)

	_, err = buf.ReadFrom(w.file)
	if err != nil {
		return nil, err
	}

	var entries []*internalpb.Entry
	reader := bytes.NewReader(buf.Bytes())
	for reader.Len() > 0 {
		// data length
		var n int64
		if err = binary.Read(reader, binary.LittleEndian, &n); err != nil {
			return nil, err
		}

		// data body
		data := make([]byte, n)
		if err = binary.Read(reader, binary.LittleEndian, &data); err != nil {
			return nil, err
		}

		entry := new(internalpb.Entry)
		if err = proto.Unmarshal(data, entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (w *WAL) Version() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.version
}

func (w *WAL) close() error {
	// w.file will be nil if close is already called
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}
	return nil
}

func ParseVersion(file string) string {
	parts := strings.Split(strings.TrimSuffix(file, ".log"), "-")
	return fmt.Sprintf("%s-%s", parts[1], parts[2])
}

func CompareVersion(v1, v2 string) int {
	parts1 := strings.Split(v1, "-")
	parts2 := strings.Split(v2, "-")

	if parts1[0] < parts2[0] {
		return -1
	} else if parts1[0] > parts2[0] {
		return 1
	}

	if parts1[1] < parts2[1] {
		return -1
	} else if parts1[1] > parts2[1] {
		return 1
	}

	return 0
}
