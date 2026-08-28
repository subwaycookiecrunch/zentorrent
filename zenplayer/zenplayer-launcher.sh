#!/bin/bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Check if local venv exists
if [ ! -d "$DIR/venv" ]; then
    echo "ZenPlayer: Initializing virtual environment..."
    python3 -m venv "$DIR/venv"
    "$DIR/venv/bin/pip" install --quiet -r "$DIR/requirements.txt"
fi

# Run with arguments passed through
exec "$DIR/venv/bin/python" "$DIR/zenplayer.py" "$@"
