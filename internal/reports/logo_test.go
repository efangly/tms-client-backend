package reports

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLogoPath_EnvOverrideAndDefault(t *testing.T) {
	t.Setenv("LOGO_PATH", "")
	if got := LogoPath(); got != filepath.Join("assets", "logo.png") {
		t.Errorf("default = %q", got)
	}
	t.Setenv("LOGO_PATH", "custom/mylogo.png")
	if got := LogoPath(); got != "custom/mylogo.png" {
		t.Errorf("override = %q", got)
	}
}

func TestEnsureLogoFile_CreatesButNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assets", "logo.png")
	t.Setenv("LOGO_PATH", path)

	if err := EnsureLogoFile(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, defaultLogo) {
		t.Fatalf("default logo not written (err=%v)", err)
	}

	custom := []byte("customer logo")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLogoFile(); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, custom) {
		t.Error("EnsureLogoFile overwrote an existing logo")
	}
}

func TestLoadLogo_PrefersFileFallsBackToEmbedded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logo.png")
	t.Setenv("LOGO_PATH", path)

	if got := LoadLogo(); !bytes.Equal(got, defaultLogo) {
		t.Error("missing file should fall back to the embedded logo")
	}
	custom := []byte("replacement")
	_ = os.WriteFile(path, custom, 0o644)
	if got := LoadLogo(); !bytes.Equal(got, custom) {
		t.Error("file on disk should win over the embedded logo")
	}
}

func TestDefaultLogoIsValidImage(t *testing.T) {
	if _, ok := decodeLogo(defaultLogo); !ok {
		t.Fatal("embedded logo must be a decodable PNG/JPEG")
	}
	if _, ok := decodeLogo([]byte("not an image")); ok {
		t.Error("garbage must be rejected")
	}
}
