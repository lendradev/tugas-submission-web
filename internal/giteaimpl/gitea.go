// Package giteaimpl berisi implementasi Pusher untuk server Gitea.
//
// Gitea tidak menyediakan Git Data API lengkap (tidak ada POST git/blobs dan
// POST git/trees), sehingga push dilakukan lewat Contents API: satu commit per
// berkas. Implementasi ini dipakai untuk pengujian lokal terhadap Gitea;
// produksi memakai Package github yang memakai Git Data API GitHub.
package giteaimpl

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	syncer "github.com/lendradev/tugas-submission/internal/sync"
)

// Client adalah pusher berbasis Contents API Gitea.
type Client struct {
	HTTP   *http.Client
	Owner  string
	Repo   string
	Token  string
	API    string
	Branch string
}

// New membuat pusher Gitea.
func New(owner, repo, token, branch, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:3000"
	}
	if branch == "" {
		branch = "main"
	}
	return &Client{
		HTTP:   &http.Client{Timeout: 60 * time.Second},
		Owner:  owner,
		Repo:   repo,
		Token:  token,
		API:    strings.TrimRight(baseURL, "/"),
		Branch: branch,
	}
}

// APIError adalah kesalahan dari API Gitea.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return fmt.Sprintf("Gitea API %d: %s", e.Status, e.Msg) }

// Ready memberi tahu apakah konfigurasi repo lengkap.
func (c *Client) Ready() bool {
	return c.Owner != "" && c.Repo != "" && c.Token != ""
}

// Describe mengembalikan keterangan repo untuk UI.
func (c *Client) Describe() string {
	if !c.Ready() {
		return ""
	}
	return c.Owner + "/" + c.Repo + " (" + c.Branch + ", Gitea)"
}

func (c *Client) repoPath(suffix string) string {
	return "/api/v1/repos/" + url.PathEscape(c.Owner) + "/" + url.PathEscape(c.Repo) + suffix
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.API+path, reader)
	if err != nil {
		return nil, 0, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "token "+c.Token)
	}
	req.Header.Set("User-Agent", "tugas-submission")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		var er struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &er)
		if er.Message == "" {
			er.Message = strings.TrimSpace(string(data))
		}
		return nil, resp.StatusCode, &APIError{Status: resp.StatusCode, Msg: er.Message}
	}
	return data, resp.StatusCode, nil
}

// fileResponse adalah hasil operasi Contents API.
//
// Field "content" pada Gitea bisa berupa string (base64) atau objek.
// Hanya field "sha" yang dipakai di sini.
type fileResponse struct {
	Content json.RawMessage `json:"content"`
	SHA     string          `json:"sha"`
	Commit  struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// Push menjalankan operasi berkas lewat Contents API Gitea.
func (c *Client) Push(ctx context.Context, username string, ops []syncer.FileOp) (map[string]string, error) {
	shas := make(map[string]string, len(ops))
	for _, op := range ops {
		repoPath := "tugas/" + username + "/" + op.Path
		if op.Delete {
			if err := c.deleteFile(ctx, repoPath); err != nil {
				return nil, err
			}
			continue
		}
		sha, err := c.putFile(ctx, repoPath, op.Content)
		if err != nil {
			return nil, err
		}
		shas[op.Path] = sha
	}
	return shas, nil
}

// putFile membuat atau memperbarui satu berkas.
func (c *Client) putFile(ctx context.Context, repoPath string, data []byte) (string, error) {
	body := map[string]any{
		"content":  base64.StdEncoding.EncodeToString(data),
		"message":  "sinkronisasi tugas: " + repoPath,
		"branch":   c.Branch,
		"encoding": "base64",
	}
	// Overwrite berkas yang sudah ada memerlukan SHA blob terkini.
	if sha, err := c.getFileSHA(ctx, repoPath); err == nil && sha != "" {
		body["sha"] = sha
	}
	resp, _, err := c.do(ctx, http.MethodPut, c.repoPath("/contents/"+repoPath), body)
	if err != nil {
		return "", fmt.Errorf("tulis %s: %w", repoPath, err)
	}
	var fr fileResponse
	if err := json.Unmarshal(resp, &fr); err != nil {
		return "", err
	}
	if fr.SHA != "" {
		return fr.SHA, nil
	}
	var content struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(fr.Content, &content); err == nil && content.SHA != "" {
		return content.SHA, nil
	}
	return "", nil
}

// getFileSHA mengambil SHA blob terkini untuk overwrite.
func (c *Client) getFileSHA(ctx context.Context, repoPath string) (string, error) {
	data, status, err := c.do(ctx, http.MethodGet, c.repoPath("/contents/"+repoPath), nil)
	if err != nil {
		if status == http.StatusNotFound {
			return "", ErrNotFound
		}
		return "", err
	}
	var fr fileResponse
	if err := json.Unmarshal(data, &fr); err != nil {
		return "", err
	}
	if fr.SHA != "" {
		return fr.SHA, nil
	}
	// Fallback: SHA ada di dalam objek content.
	var content struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(fr.Content, &content); err == nil && content.SHA != "" {
		return content.SHA, nil
	}
	return "", nil
}

// deleteFile menghapus satu berkas dari repository.
func (c *Client) deleteFile(ctx context.Context, repoPath string) error {
	sha, err := c.getFileSHA(ctx, repoPath)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return fmt.Errorf("hapus %s: %w", repoPath, err)
	}
	body := map[string]any{
		"message": "hapus berkas: " + repoPath,
		"branch":  c.Branch,
		"sha":     sha,
	}
	_, _, err = c.do(ctx, http.MethodDelete, c.repoPath("/contents/"+repoPath), body)
	return err
}

// ErrNotFound dikembalikan saat berkas tidak ada di repository.
var ErrNotFound = errors.New("berkas tidak ditemukan")

// DownloadBlob streaming isi blob dari Gitea ke writer.
func (c *Client) DownloadBlob(ctx context.Context, sha string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.API+c.repoPath("/git/blobs/"+url.PathEscape(sha)), nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "token "+c.Token)
	}
	req.Header.Set("User-Agent", "tugas-submission")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &APIError{Status: resp.StatusCode, Msg: strings.TrimSpace(string(body))}
	}
	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if payload.Encoding != "base64" {
		return fmt.Errorf("encoding blob tidak didukung: %s", payload.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(payload.Content, "\n", ""))
	if err != nil {
		return err
	}
	_, err = w.Write(decoded)
	return err
}
