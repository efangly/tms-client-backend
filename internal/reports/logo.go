package reports

import (
	"bytes"
	_ "embed"
	"image"
	_ "image/jpeg" // register decoders so decodeLogo can validate JPEG logos
	_ "image/png"  // and PNG logos
	"log"
	"os"
	"path/filepath"
)

// defaultLogo is the built-in logo, used when no logo file exists on disk.
//
//go:embed assets/logo.png
var defaultLogo []byte

// LogoPath is where the report logo is read from: env LOGO_PATH, else
// assets/logo.png next to the binary (the working directory is the exe
// directory, as for reports/ and archives/).
func LogoPath() string {
	if p := os.Getenv("LOGO_PATH"); p != "" {
		return p
	}
	return filepath.Join("assets", "logo.png")
}

// EnsureLogoFile creates the logo folder and seeds it with the built-in logo
// if no file is there yet. An existing file is never overwritten.
func EnsureLogoFile() error {
	path := LogoPath()
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, defaultLogo, 0o644)
}

// LoadLogo returns the logo bytes, read fresh each call so replacing the
// file takes effect without a restart. It falls back to the built-in logo
// when the file is missing or unreadable.
func LoadLogo() []byte {
	b, err := os.ReadFile(LogoPath())
	if err != nil || len(b) == 0 {
		return defaultLogo
	}
	return b
}

// decodeLogo reports the image format ("png" or "jpeg") if b is a
// supported, decodable image.
func decodeLogo(b []byte) (format string, ok bool) {
	if len(b) == 0 {
		return "", false
	}
	if _, f, err := image.DecodeConfig(bytes.NewReader(b)); err == nil && (f == "png" || f == "jpeg") {
		return f, true
	}
	return "", false
}

func warnBadLogo() {
	log.Printf("report: logo at %s is not a valid PNG/JPEG, generating the report without it", LogoPath())
}
