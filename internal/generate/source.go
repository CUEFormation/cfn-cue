package generate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const LatestURL = "https://github.com/aws-cloudformation/resource-provider-enhanced-schemas/releases/download/latest/schemas-cfn-lint.zip"
const maxArchiveBytes int64 = 128 << 20

type Source struct {
	files         map[string][]byte
	Label, SHA256 string
}

func OpenSource(ctx context.Context, location string) (*Source, error) {
	s := &Source{files: map[string][]byte{}, Label: location}
	var data []byte
	var err error
	if strings.HasPrefix(location, "https://") {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
		if e != nil {
			return nil, e
		}
		client := &http.Client{Timeout: 2 * time.Minute}
		resp, e := client.Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("download %s: %s", location, resp.Status)
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
		if len(data) > int(maxArchiveBytes) {
			return nil, fmt.Errorf("schema archive exceeds %d bytes", maxArchiveBytes)
		}
	} else {
		info, e := os.Stat(location)
		if e != nil {
			return nil, e
		}
		if info.IsDir() {
			for _, dir := range []string{"providers", "resources"} {
				entries, e := os.ReadDir(filepath.Join(location, dir))
				if e != nil {
					return nil, e
				}
				for _, entry := range entries {
					if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
						b, e := os.ReadFile(filepath.Join(location, dir, entry.Name()))
						if e != nil {
							return nil, e
						}
						s.files[dir+"/"+entry.Name()] = b
					}
				}
			}
			s.SHA256 = hashFiles(s.files)
			return s, nil
		}
		data, err = os.ReadFile(location)
	}
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(data)
	s.SHA256 = hex.EncodeToString(h[:])
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var total uint64
	for _, entry := range z.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name := entry.Name
		if !(strings.HasPrefix(name, "providers/") || strings.HasPrefix(name, "resources/")) {
			continue
		}
		if filepath.ToSlash(filepath.Clean(name)) != name || strings.Count(name, "/") != 1 || !strings.HasSuffix(name, ".json") {
			return nil, fmt.Errorf("invalid archive member %q", name)
		}
		total += entry.UncompressedSize64
		if total > 512<<20 {
			return nil, fmt.Errorf("uncompressed archive too large")
		}
		if _, exists := s.files[name]; exists {
			return nil, fmt.Errorf("duplicate archive member %q", name)
		}
		r, e := entry.Open()
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(io.LimitReader(r, 16<<20))
		closeErr := r.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(b) >= 16<<20 {
			return nil, fmt.Errorf("schema member too large: %s", name)
		}
		s.files[name] = b
	}
	return s, nil
}
func hashFiles(files map[string][]byte) string {
	h := sha256.New()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(h, "%d:%s:%d:", len(n), n, len(files[n]))
		h.Write(files[n])
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Source) read(name string) ([]byte, error) {
	b, ok := s.files[name]
	if !ok {
		return nil, fmt.Errorf("source missing %s", name)
	}
	return b, nil
}
