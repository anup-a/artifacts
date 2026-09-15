//go:build ignore

// e2e-publish posts a directory to a running artifacts server.
// Usage: go run ./scripts/e2e-publish.go <base> <token> <dir> [artifact-id]
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: e2e-publish <base> <token> <dir> [id]")
		os.Exit(2)
	}
	base, token, dir := os.Args[1], os.Args[2], os.Args[3]
	id := ""
	if len(os.Args) > 4 {
		id = os.Args[4]
	}

	entries, err := os.ReadDir(dir)
	check(err)

	type mf struct {
		Path   string `json:"path"`
		Sha256 string `json:"sha256"`
		Size   int    `json:"size"`
	}
	var files []mf
	bodies := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		check(err)
		sum := sha256.Sum256(b)
		h := hex.EncodeToString(sum[:])
		files = append(files, mf{Path: e.Name(), Sha256: h, Size: len(b)})
		bodies[h] = b
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	manifest, _ := json.Marshal(map[string]any{"files": files})
	fw, err := w.CreateFormField("manifest")
	check(err)
	fw.Write(manifest)
	for h, b := range bodies {
		fw, err := w.CreateFormFile(h, h)
		check(err)
		fw.Write(b)
	}
	check(w.Close())

	method, url := http.MethodPost, base+"/api/artifacts"
	if id != "" {
		method, url = http.MethodPut, base+"/api/artifacts/"+id
	}
	req, err := http.NewRequest(method, url, &buf)
	check(err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	check(err)
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "publish failed %d: %s\n", res.StatusCode, out)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
