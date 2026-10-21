#!/bin/bash

# Build script for TMS Go Backend
# Builds for Windows deployment as a system tray application

echo "🔨 Building TMS Go Backend for Windows (System Tray)..."

cd "$(dirname "$0")"

# Set build environment for Windows
export GOOS=windows
export GOARCH=amd64
export CGO_ENABLED=1

# Build binary with -H windowsgui to hide console window (runs as tray app)
echo "📦 Building for Windows (amd64) as system tray app..."
# Version precedence: shell env VERSION > VERSION in .env > git describe > "dev"
if [ -z "$VERSION" ] && [ -f .env ]; then
    VERSION="$(grep -E '^VERSION=' .env | tail -n1 | cut -d= -f2- | tr -d '\r"'"'"' ')"
fi
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
echo "🏷️  Version: $VERSION"
go build -ldflags="-s -w -H windowsgui -X tms-backend/internal/version.Version=$VERSION" -o ./build/tms-backend.exe ./main.go

if [ $? -eq 0 ]; then
    echo "✅ Build successful!"
    echo "📁 Output: build/tms-backend.exe"
    
    # Seed build/.env from .env.example only if it doesn't exist yet, so an
    # already-configured build/.env (DB credentials etc.) is never overwritten.
    if [ -f ./build/.env ]; then
        echo "ℹ️  build/.env already exists - keeping it"
    else
        cp .env.example ./build/.env 2>/dev/null
        echo "⚠️  build/.env created from .env.example - fill in DB_HOST, DB_PASSWORD, etc. before running"
    fi
    
    echo ""
    echo "📝 Deployment instructions:"
    echo "   1. Copy build/tms-backend.exe to Windows server"
    echo "   2. Create .env file with database credentials"
    echo "   3. Double-click tms-backend.exe to run"
    echo "   4. The application will appear in the system tray"
    echo "   5. Right-click the tray icon for options"
    echo "   6. Click 'Exit' from the tray menu to stop the application"
    echo "   7. Logs are saved in the logs/ folder"
    echo ""
else
    echo "❌ Build failed!"
    exit 1
fi
