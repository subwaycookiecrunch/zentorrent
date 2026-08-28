import yt_dlp

def search_youtube(query: str, max_results: int = 15):
    opts = {
        'format': 'bestaudio/best',
        'quiet': True,
        'extract_flat': 'in_playlist',
        'default_search': 'ytsearch',
    }
    res = []
    try:
        with yt_dlp.YoutubeDL(opts) as ydl:
            info = ydl.extract_info(f"ytsearch{max_results}:{query}", download=False)
            if 'entries' in info:
                for entry in info['entries']:
                    if not entry:
                        continue
                    res.append({
                        'title': entry.get('title', 'Unknown Title'),
                        'uploader': entry.get('uploader', 'Unknown Artist'),
                        'duration': entry.get('duration', 0),
                        'id': entry.get('id', ''),
                        'url': entry.get('url', '')
                    })
    except Exception:
        pass
    return res

def format_duration(seconds: int) -> str:
    if not seconds:
        return "0:00"
    return f"{int(seconds // 60)}:{int(seconds % 60):02d}"

if __name__ == "__main__":
    for r in search_youtube("jazz radio"):
        print(f"{r['title']} - {format_duration(r['duration'])}")
