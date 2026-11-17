// Package github adalah klien REST API GitHub (Git Data API) tanpa dependensi eksternal.
//
// Klien juga dapat diarahkan ke server kompatibel (mis. Gitea) untuk pengujian lokal:
// perbedaan bentuk respons ditangani lewat opsi Client.Gitea.
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	syncer "github.com/lendradev/tugas-submission/internal/sync"
)

// Client memanggil GitHub REST API memakai fine-grained personal access token.
type Client struct {
	HTTP  *http.Client
	Owner string
	Repo  string
	Token string
	API   string
	// Branch adalah branch tujuan push.
	Branch string
	// Gitea mengaktifkan mode kompatibilitas server Gitea.
	Gitea bool
}

// New membuat klien. baseURL dapat dikosongkan untuk memakai api.github.com.
func New(owner, repo, token, branch, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://api.github.com"
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

// ErrNotFound dikembalikan saat resource tidak ada.
var ErrNotFound = errors.New("resource tidak ditemukan")

// ErrRateLimited dikembalikan saat GitHub membalas 429 / 403 rate limit.
var ErrRateLimited = errors.New("rate limit GitHub")

// APIError adalah kesalahan dari GitHub API.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return fmt.Sprintf("GitHub API %d: %s", e.Status, e.Msg) }

// Retryable memberi tahu apakah permintaan boleh dicoba ulang.
func (e *APIError) Retryable() bool {
	return e.Status == 429 || e.Status == 403 || e.Status >= 500
}

type errorResponse struct {
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, accept string) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.API+path, reader)
	if err != nil {
		return nil, 0, err
	}
	c.setHeaders(req, accept)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		var er errorResponse
		_ = json.Unmarshal(data, &er)
		if er.Message == "" {
			er.Message = strings.TrimSpace(string(data))
		}
		switch resp.StatusCode {
		case 404:
			return nil, resp.StatusCode, ErrNotFound
		case 429:
			return nil, resp.StatusCode, ErrRateLimited
		}
		return nil, resp.StatusCode, &APIError{Status: resp.StatusCode, Msg: er.Message}
	}
	return data, resp.StatusCode, nil
}

func (c *Client) setHeaders(req *http.Request, accept string) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "tugas-submission")
	if accept == "" {
		accept = "application/vnd.github+json"
	}
	req.Header.Set("Accept", accept)
}

func (c *Client) repoPath(suffix string) string {
	return "/repos/" + url.PathEscape(c.Owner) + "/" + url.PathEscape(c.Repo) + suffix
}

// Ref adalah referensi branch di repository.
type Ref struct {
	Ref    string `json:"ref"`
	Object struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
	} `json:"object"`
}

// TreeEntry adalah satu entri dalam tree.
type TreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

// TreeResult adalah hasil pembuatan/pembacaan tree.
type TreeResult struct {
	SHA       string      `json:"sha"`
	Entries   []TreeEntry `json:"tree"`
	Truncated bool        `json:"truncated"`
}

// BlobResult adalah hasil pembuatan blob.
type BlobResult struct {
	SHA string `json:"sha"`
}

// CommitResult adalah hasil pembuatan commit.
type CommitResult struct {
	SHA string `json:"sha"`
}

// GetRef mengambil ref branch. Pada Gitea endpoint mengembalikan array.
func (c *Client) GetRef(ctx context.Context, branch string) (Ref, error) {
	data, _, err := c.do(ctx, http.MethodGet, c.repoPath("/git/ref/heads/"+url.PathEscape(branch)), nil, "")
	if err != nil {
		return Ref{}, err
	}
	if c.Gitea {
		var list []Ref
		if err := json.Unmarshal(data, &list); err != nil {
			return Ref{}, err
		}
		if len(list) == 0 {
			return Ref{}, fmt.Errorf("branch %s tidak ada", branch)
		}
		return list[0], nil
	}
	var r Ref
	if err := json.Unmarshal(data, &r); err != nil {
		return Ref{}, err
	}
	if r.Object.SHA == "" {
		return Ref{}, fmt.Errorf("ref %s tidak punya object sha", branch)
	}
	return r, nil
}

// CreateBlob mengunggah isi blob (base64) dan mengembalikan SHA-nya.
func (c *Client) CreateBlob(ctx context.Context, content []byte) (BlobResult, error) {
	body := map[string]string{
		"content":  base64.StdEncoding.EncodeToString(content),
		"encoding": "base64",
	}
	data, _, err := c.do(ctx, http.MethodPost, c.repoPath("/git/blobs"), body, "")
	if err != nil {
		return BlobResult{}, err
	}
	var r BlobResult
	if err := json.Unmarshal(data, &r); err != nil {
		return BlobResult{}, err
	}
	if r.SHA == "" {
		return BlobResult{}, errors.New("blob tanpa sha")
	}
	return r, nil
}

// CreateTree membuat tree baru berdasarkan base tree.
func (c *Client) CreateTree(ctx context.Context, baseTree string, entries []TreeEntry) (TreeResult, error) {
	body := map[string]any{
		"base_tree": baseTree,
		"tree":      entries,
	}
	data, _, err := c.do(ctx, http.MethodPost, c.repoPath("/git/trees"), body, "")
	if err != nil {
		return TreeResult{}, err
	}
	var r TreeResult
	if err := json.Unmarshal(data, &r); err != nil {
		return TreeResult{}, err
	}
	return r, nil
}

// CommitInput adalah payload pembuatan commit.
type CommitInput struct {
	Message string   `json:"message"`
	Tree    string   `json:"tree"`
	Parents []string `json:"parents"`
}

// CreateCommit membuat commit di atas parent yang diberikan.
func (c *Client) CreateCommit(ctx context.Context, in CommitInput) (CommitResult, error) {
	data, _, err := c.do(ctx, http.MethodPost, c.repoPath("/git/commits"), in, "")
	if err != nil {
		return CommitResult{}, err
	}
	var r CommitResult
	if err := json.Unmarshal(data, &r); err != nil {
		return CommitResult{}, err
	}
	if r.SHA == "" {
		return CommitResult{}, errors.New("commit tanpa sha")
	}
	return r, nil
}

// UpdateRef menunjukkan ref branch ke SHA baru.
func (c *Client) UpdateRef(ctx context.Context, branch, sha string, force bool) error {
	body := map[string]any{"sha": sha, "force": force}
	_, _, err := c.do(ctx, http.MethodPatch,
		c.repoPath("/git/refs/heads/"+url.PathEscape(branch)), body, "")
	return err
}

// CommitTree mengembalikan SHA tree dari sebuah commit.
func (c *Client) CommitTree(ctx context.Context, commitSHA string) (string, error) {
	data, _, err := c.do(ctx, http.MethodGet, c.repoPath("/git/commits/"+url.PathEscape(commitSHA)), nil, "")
	if err != nil {
		return "", err
	}
	if c.Gitea {
		var payload struct {
			Commit struct {
				Tree struct {
					SHA string `json:"sha"`
				} `json:"tree"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return "", err
		}
		if payload.Commit.Tree.SHA == "" {
			return "", errors.New("commit tidak memuat tree")
		}
		return payload.Commit.Tree.SHA, nil
	}
	var payload struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	if payload.Tree.SHA == "" {
		return "", errors.New("commit tidak memuat tree")
	}
	return payload.Tree.SHA, nil
}

// Push pushing sekumpulan operasi berkas memakai Git Data API GitHub.
//
// Setiap berkas menjadi satu blob + satu entri tree; seluruh entri dikumpulkan
// dalam satu tree dan satu commit agar history repo tetap rapi.
func (c *Client) Push(ctx context.Context, username string, ops []syncer.FileOp) (map[string]string, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	ref, err := c.GetRef(ctx, c.Branch)
	if err != nil {
		return nil, fmt.Errorf("ambil ref: %w", err)
	}
	baseTree, err := c.CommitTree(ctx, ref.Object.SHA)
	if err != nil {
		return nil, fmt.Errorf("ambil tree dasar: %w", err)
	}

	entries := make([]TreeEntry, 0, len(ops))
	shas := make(map[string]string, len(ops))
	for _, op := range ops {
		repoPath := path.Join("tugas", username, op.Path)
		if op.Delete {
			entries = append(entries, TreeEntry{Path: repoPath, Mode: "100644", Type: "blob", SHA: ""})
			continue
		}
		blob, err := c.CreateBlob(ctx, op.Content)
		if err != nil {
			return nil, fmt.Errorf("buat blob %s: %w", op.Path, err)
		}
		entries = append(entries, TreeEntry{Path: repoPath, Mode: "100644", Type: "blob", SHA: blob.SHA})
		shas[op.Path] = blob.SHA
	}

	tree, err := c.CreateTree(ctx, baseTree, entries)
	if err != nil {
		return nil, fmt.Errorf("buat tree: %w", err)
	}
	commit, err := c.CreateCommit(ctx, CommitInput{
		Message: fmt.Sprintf("sinkronisasi tugas (%d berkas)", len(entries)),
		Tree:    tree.SHA,
		Parents: []string{ref.Object.SHA},
	})
	if err != nil {
		return nil, fmt.Errorf("buat commit: %w", err)
	}
	if err := c.UpdateRef(ctx, c.Branch, commit.SHA, false); err != nil {
		return nil, fmt.Errorf("perbarui ref: %w", err)
	}
	return shas, nil
}

// Ready memberi tahu apakah konfigurasi repo lengkap.
func (c *Client) Ready() bool {
	return c.Owner != "" && c.Repo != "" && c.Token != ""
}

// Describe mengembalikan keterangan repo untuk UI.
func (c *Client) Describe() string {
	if !c.Ready() {
		return ""
	}
	return c.Owner + "/" + c.Repo + " (" + c.Branch + ")"
}

// DownloadBlob streams isi blob ke writer (hemat memori untuk berkas besar).
//
// GitHub mengembalikan isi mentah; Gitea mengembalikan JSON berisi base64.
// Keduanya ditangani di sini.
func (c *Client) DownloadBlob(ctx context.Context, sha string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.API+c.repoPath("/git/blobs/"+url.PathEscape(sha)), nil)
	if err != nil {
		return err
	}
	c.setHeaders(req, "application/vnd.github.raw")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &APIError{Status: resp.StatusCode, Msg: strings.TrimSpace(string(body))}
	}

	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "json") {
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

	_, err = io.Copy(w, resp.Body)
	return err
}

// RateLimitRemaining memberi tahu sisa kuota API.
func (c *Client) RateLimitRemaining(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.API+"/rate_limit", nil)
	if err != nil {
		return 0, err
	}
	c.setHeaders(req, "")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("rate_limit: status %d", resp.StatusCode)
	}
	var payload struct {
		Resources struct {
			Core struct {
				Limit     int `json:"limit"`
				Remaining int `json:"remaining"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}
	return payload.Resources.Core.Remaining, nil
}
