package version

// Version is overridden at build time via
// -ldflags "-X tms-backend/internal/version.Version=1.0.0".
var Version = "dev"
