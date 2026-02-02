#!/bin/bash

# Build script for TMS Go Backend
# Builds for Windows deployment with console window

echo "🔨 Building TMS Go Backend for Windows (with console)..."

cd "$(dirname "$0")"

# Set build environment for Windows
export GOOS=windows
export GOARCH=amd64
export CGO_ENABLED=0

# Build binary with console window support
# No -H windowsgui flag = console window will be shown
echo "📦 Building for Windows (amd64) with console window..."
go build -ldflags="-s -w" -o ./build/tms-backend.exe ./main.go

if [ $? -eq 0 ]; then
    echo "✅ Build successful!"
    echo "📁 Output: build/tms-backend.exe"
    
    # Copy .env.example
    cp .env.example ./build/.env 2>/dev/null
    
    echo ""
    echo "📝 Deployment instructions:"
    echo "   1. Copy build/tms-backend.exe to Windows server"
    echo "   2. Create .env file with database credentials"
    echo "   3. Double-click tms-backend.exe to run"
    echo "   4. Console window will appear with server logs"
    echo "   5. Close the console window to stop the application"
    echo "   6. Or press Ctrl+C in the console to stop gracefully"
    echo ""
else
    echo "❌ Build failed!"
    exit 1
fi
