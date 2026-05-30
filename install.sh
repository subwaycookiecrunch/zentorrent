#!/bin/bash
set -e

# ZenTorrent Install Script (Mac & Linux)
echo "⚡ Installing ZenTorrent..."

OS="$(uname -s)"
ARCH="$(uname -m)"

# Determine correct binary name based on OS and architecture
if [ "$OS" = "Darwin" ]; then
    if [ "$ARCH" = "arm64" ]; then
        BINARY="zentorrent-darwin-arm64"
    else
        BINARY="zentorrent-darwin-amd64"
    fi
elif [ "$OS" = "Linux" ]; then
    BINARY="zentorrent-linux-amd64"
else
    echo "Unsupported OS: $OS"
    exit 1
fi

# We assume you will replace YOUR_GITHUB_USERNAME with your actual GitHub name.
REPO="YOUR_GITHUB_USERNAME/zentorrent"

echo "Downloading latest release for $OS ($ARCH)..."
# Fetch latest release URL from GitHub API
LATEST_URL=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep "browser_download_url.*$BINARY" | cut -d '"' -f 4)

if [ -z "$LATEST_URL" ]; then
    echo "Could not find latest release for $BINARY. Ensure the repository has releases published."
    exit 1
fi

curl -sL "$LATEST_URL" -o /tmp/zentorrent
chmod +x /tmp/zentorrent

echo "Installing to /usr/local/bin (may require sudo password)..."
sudo mv /tmp/zentorrent /usr/local/bin/zentorrent

echo "✅ ZenTorrent successfully installed! Type 'zentorrent' to run."
