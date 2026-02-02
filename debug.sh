#!/bin/bash
# Debug script to test the application

echo "=== TMS Backend Debug Test ==="
echo ""

# Check if .env file exists
if [ -f ".env" ]; then
    echo "✅ .env file exists"
else
    echo "❌ .env file NOT found"
    echo "Creating sample .env file..."
    cat > .env << 'EOF'
# Server Configuration
PORT=8080

# Database Configuration
DB_HOST=localhost
DB_PORT=3306
DB_USER=root
DB_PASSWORD=password
DB_NAME=tms

# Polling Configuration
POLL_INTERVAL=5m
ALERT_INTERVAL=5s
EOF
    echo "✅ Sample .env file created"
fi

echo ""
echo "=== Building application ==="
go build -o tms-backend-debug main.go

if [ $? -eq 0 ]; then
    echo "✅ Build successful"
    echo ""
    echo "=== Running application ==="
    ./tms-backend-debug
else
    echo "❌ Build failed"
    exit 1
fi
