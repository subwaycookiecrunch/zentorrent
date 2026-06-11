#!/bin/bash
set -e

echo "⚡ Installing ZenTorrent..."

OS="$(uname -s)"
ARCH="$(uname -m)"

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

REPO="subwaycookiecrunch/zentorrent"

echo "Downloading latest release for $OS ($ARCH)..."
LATEST_URL=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep "browser_download_url.*$BINARY" | cut -d '"' -f 4)

if [ -z "$LATEST_URL" ]; then
    echo "Could not find latest release for $BINARY"
    exit 1
fi

curl -sL "$LATEST_URL" -o /tmp/zentorrent
chmod +x /tmp/zentorrent

echo "Installing to /usr/local/bin (may need sudo)..."
sudo mv /tmp/zentorrent /usr/local/bin/zentorrent

echo "✅ Done. Run 'zentorrent' to start."
