package updater

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// AssetName is the release asset holding the Windows binary.
const AssetName = "tms-backend.exe"

// ChecksumsName is the release asset listing sha256 sums ("<hex>  <file>").
const ChecksumsName = "checksums.txt"

// maxExeSize bounds the download so a bad asset can't fill the disk.
const maxExeSize = 200 << 20

// release is the part of a GitHub release the updater needs.
type release struct {
	Version     string // tag without the leading "v"
	ExeURL      string
	ChecksumURL string
}

type ghRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (s *Service) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tms-backend-updater/"+s.current)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

// fetchLatest reads the latest published release from the GitHub API.
func (s *Service) fetchLatest(ctx context.Context) (release, error) {
	resp, err := s.get(ctx, s.cfg.APIURL)
	if err != nil {
		return release{}, err
	}
	defer resp.Body.Close()
	var gr ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&gr); err != nil {
		return release{}, fmt.Errorf("decode release: %w", err)
	}
	if gr.Draft || gr.Prerelease {
		return release{}, errors.New("latest release is a draft or pre-release")
	}
	r := release{Version: strings.TrimPrefix(gr.TagName, "v")}
	for _, a := range gr.Assets {
		switch a.Name {
		case AssetName:
			r.ExeURL = a.URL
		case ChecksumsName:
			r.ChecksumURL = a.URL
		}
	}
	if r.ExeURL == "" || r.ChecksumURL == "" {
		return release{}, fmt.Errorf("release %s is missing %s or %s", gr.TagName, AssetName, ChecksumsName)
	}
	return r, nil
}

// parseChecksum finds the sha256 for name in a sha256sum-style listing.
func parseChecksum(r io.Reader, name string) (string, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			sum := strings.ToLower(f[0])
			if len(sum) != 64 {
				return "", fmt.Errorf("bad checksum for %s", name)
			}
			return sum, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

// download fetches the release exe to dst, verified against checksums.txt.
// dst is only created once the hash matches.
func (s *Service) download(ctx context.Context, r release, dst string) error {
	resp, err := s.get(ctx, r.ChecksumURL)
	if err != nil {
		return err
	}
	want, err := parseChecksum(io.LimitReader(resp.Body, 1<<20), AssetName)
	resp.Body.Close()
	if err != nil {
		return err
	}

	resp, err = s.get(ctx, r.ExeURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxExeSize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxExeSize {
		err = fmt.Errorf("download exceeds %d bytes", maxExeSize)
	}
	if err == nil {
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			err = fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
		}
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	os.Remove(dst)
	return os.Rename(tmp, dst)
}
