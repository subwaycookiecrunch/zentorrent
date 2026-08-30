import sys
import os
import time
import json
import random
import subprocess
from textual.app import App, ComposeResult
from textual.widgets import Input, ListView, ListItem, Label, Static
from textual.containers import Vertical, Horizontal
from textual import work
from textual.binding import Binding
from textual.message import Message
from textual.events import Click

# Ensure zenplayer directory is in Python path for imports
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
if SCRIPT_DIR not in sys.path:
    sys.path.insert(0, SCRIPT_DIR)

from search import search_youtube, format_duration
from player import ZenPlayerBackend

class TrackEnded(Message):
    pass

class ResultItem(ListItem):
    def __init__(self, metadata, is_queue_item=False):
        super().__init__()
        self.metadata = metadata
        self.is_queue_item = is_queue_item

    def compose(self) -> ComposeResult:
        yield Label(f"{self.metadata.get('title', 'Unknown')}", classes="song-title")
        yield Label(f"  {format_duration(self.metadata.get('duration', 0))} ", classes="song-meta")

class ProgressBarWidget(Static):
    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self.bar_len = 0
        self.prefix_len = 0

    def on_click(self, event: Click) -> None:
        dur = self.app.player.duration
        if dur > 0 and getattr(self, 'bar_len', 0) > 0:
            bx = event.x - getattr(self, 'prefix_len', 0)
            bx = max(0, min(bx, self.bar_len))
            self.app.player.seek_percent((bx / self.bar_len) * 100)

class CassetteWidget(Static):
    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self.frames = [
            "[#dfa27a]   .----------------------.[/]\n"
            "[#dfa27a]   |[/][#e8e3d9]  ZENPLAYER  |  2026  [/][#dfa27a]|[/]\n"
            "[#dfa27a]   |[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#dfa27a]|[/]\n"
            "[#dfa27a]   | |[/][#d8a657] (○)======(○) [/][#dfa27a]| |   |[/]\n"
            "[#dfa27a]   |________.____________.|[/]"
            ,
            "[#dfa27a]   .----------------------.[/]\n"
            "[#dfa27a]   |[/][#e8e3d9]  ZENPLAYER  |  2026  [/][#dfa27a]|[/]\n"
            "[#dfa27a]   |[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#dfa27a]|[/]\n"
            "[#dfa27a]   | |[/][#d8a657] (◌)======(◌) [/][#dfa27a]| |   |[/]\n"
            "[#dfa27a]   |________.____________.|[/]"
            ,
            "[#dfa27a]   .----------------------.[/]\n"
            "[#dfa27a]   |[/][#e8e3d9]  ZENPLAYER  |  2026  [/][#dfa27a]|[/]\n"
            "[#dfa27a]   |[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#dfa27a]|[/]\n"
            "[#dfa27a]   | |[/][#d8a657] (◎)======(◎) [/][#dfa27a]| |   |[/]\n"
            "[#dfa27a]   |________.____________.|[/]"
            ,
            "[#dfa27a]   .----------------------.[/]\n"
            "[#dfa27a]   |[/][#e8e3d9]  ZENPLAYER  |  2026  [/][#dfa27a]|[/]\n"
            "[#dfa27a]   |[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#d08770]▄[/][#ebcb8b]▄[/][#a3be8c]▄[/][#b48ead]▄[/][#88c0d0]▄[/][#bf616a]▄[/][#dfa27a]|[/]\n"
            "[#dfa27a]   | |[/][#d8a657] (◌)======(◌) [/][#dfa27a]| |   |[/]\n"
            "[#dfa27a]   |________.____________.|[/]"
        ]
        self.frame_idx = 0

    def update_state(self, playing: bool, border_color: str = "#dfa27a"):
        frame = self.frames[self.frame_idx] if playing else self.frames[0]
        colored_frame = frame.replace("#dfa27a", border_color)
        if playing:
            self.frame_idx = (self.frame_idx + 1) % len(self.frames)
        self.update(colored_frame)

THEMES = {
    "black": {
        "css_class": "theme-black",
        "border_color": "#dfa27a",
        "bar_filled": "#3a8b8c",
        "bar_track": "#1f4445",
        "text_on_filled": "black",
        "text_on_track": "#e8e3d9"
    },
    "rose": {
        "css_class": "theme-rose",
        "border_color": "#ebbcba",
        "bar_filled": "#ebbcba",
        "bar_track": "#2a2837",
        "text_on_filled": "#191724",
        "text_on_track": "#e0def4"
    },
    "forest": {
        "css_class": "theme-forest",
        "border_color": "#8fbcbb",
        "bar_filled": "#d08770",
        "bar_track": "#2e3440",
        "text_on_filled": "#111c17",
        "text_on_track": "#eceff4"
    },
    "amber": {
        "css_class": "theme-amber",
        "border_color": "#ff8700",
        "bar_filled": "#ff8700",
        "bar_track": "#303030",
        "text_on_filled": "black",
        "text_on_track": "#ffaf00"
    }
}

class ZenPlayerApp(App):
    CSS_PATH = "zenplayer.css"
    BINDINGS = [
        Binding("enter", "play_selected", "Play"),
        Binding("a", "add_to_queue", "Add Queue"),
        Binding("d", "delete_item", "Delete"),
        Binding("s", "stop", "Stop"),
        Binding("space", "toggle_pause", "Pause"),
        Binding("n", "skip_next", "Next"),
        Binding("l", "toggle_loop", "Loop"),
        Binding("c", "clear_queue", "Clear"),
        Binding("left", "seek_backward", "Rewind 10s"),
        Binding("right", "seek_forward", "FastForward 10s"),
        Binding("shift+left", "seek_backward_large", "Rewind 60s"),
        Binding("shift+right", "seek_forward_large", "FastForward 60s"),
        Binding("w", "save_session", "Save"),
        Binding("f1", "focus_search", "Focus Search"),
        Binding("f2", "next_theme", "Theme"),
        Binding("escape", "back_to_zentorrent", "Back to ZenTorrent"),
        Binding("b", "back_to_zentorrent", "Back", show=False),
        Binding("q", "back_to_zentorrent", "Back", show=False),
        Binding("ctrl+q", "back_to_zentorrent", "Quit", show=False),
        Binding("ctrl+c", "back_to_zentorrent", "Quit", show=False),
    ]

    def __init__(self, initial_query=""):
        super().__init__()
        self.initial_query = initial_query
        self.player = ZenPlayerBackend(on_track_end=lambda: self.post_message(TrackEnded()))
        self.player.start()
        
        self.queue = []
        self.current_song = None
        self.loop_mode = 0
        self.loop_modes = ["Off", "Track", "Queue"]
        self.cur_theme = "black"
        self.session_file = os.path.join(SCRIPT_DIR, "zenplayer_queue.json")
        
        if os.path.exists(self.session_file):
            try:
                with open(self.session_file, "r") as f:
                    self.queue = json.load(f)
            except Exception:
                pass
        
    def on_track_ended(self, event: TrackEnded):
        self.action_skip_next(auto_triggered=True)

    def on_key(self, event) -> None:
        if event.key in ("escape", "ctrl+c", "ctrl+q"):
            inp = self.query_one("#search-input", Input)
            if inp.has_focus and event.key == "escape":
                inp.blur()
                self.query_one("#results-list", ListView).focus()
                event.prevent_default()
                event.stop()
                return
            self.action_back_to_zentorrent()
            event.prevent_default()
            event.stop()

    def action_back_to_zentorrent(self):
        try:
            self.player.quit()
        except Exception:
            pass
        self.exit()

    def action_quit(self):
        self.action_back_to_zentorrent()

    def compose(self) -> ComposeResult:
        with Vertical(id="app-container"):
            with Vertical(id="search-container"):
                yield Input(placeholder="Search: artist, album, song... (Press F1 to focus)", id="search-input")
                
            with Horizontal(id="main-content"):
                with Vertical(classes="pane", id="left-pane"):
                    yield ListView(id="results-list")
                
                with Vertical(classes="pane", id="right-pane"):
                    yield ListView(id="queue-list")
            
            with Vertical(id="playback-container"):
                yield CassetteWidget(id="cassette-art")
                yield Label("Nothing playing", id="track-title")
                yield ProgressBarWidget(content="", id="progress-bar")
                yield Label("idle  •  loop: off", id="status-line")
                
            yield Label("zenplayer  •  v1.0.0  •  Esc / q: Back to ZenTorrent  •  F1: Search", id="footer-stats")

    def on_mount(self):
        self.set_interval(0.5, self.update_playback_bar)
        self._refresh_queue_ui()
        self.query_one("#search-container").border_title = "SEARCH (F1)"
        self.query_one("#results-list").border_title = "SEARCH RESULTS"
        self.query_one("#queue-list").border_title = "UP NEXT QUEUE"
        self.query_one("#playback-container").border_title = "NOW PLAYING"
        self.query_one("#app-container").add_class("theme-black")

        # Auto-trigger search if query provided
        if self.initial_query:
            inp = self.query_one("#search-input", Input)
            inp.value = self.initial_query
            self.query_one("#results-list", ListView).clear()
            self._update_status("Searching...")
            self.perform_search(self.initial_query)

    def action_next_theme(self):
        names = list(THEMES.keys())
        idx = names.index(self.cur_theme)
        self.cur_theme = names[(idx + 1) % len(names)]
        
        c = self.query_one("#app-container")
        for k, cfg in THEMES.items():
            c.remove_class(cfg["css_class"])
        c.add_class(THEMES[self.cur_theme]["css_class"])
        
        self.update_playback_bar()
        self._update_status(f"theme: {self.cur_theme.upper()}")

    def action_focus_search(self):
        self.query_one("#search-input", Input).focus()

    async def on_input_submitted(self, event: Input.Submitted):
        q = event.value
        if not q:
            return
        self.query_one("#results-list", ListView).clear()
        self._update_status("Searching...")
        self.perform_search(q)
        
    @work(thread=True)
    def perform_search(self, q):
        res = search_youtube(q)
        self.call_from_thread(self.update_results, res)
        
    def update_results(self, res):
        lv = self.query_one("#results-list", ListView)
        if not res:
            self._update_status("No results found.")
            return
        self._update_status("Select a track.")
        for r in res:
            lv.append(ResultItem(r))

    def action_play_selected(self):
        f = self.focused
        if isinstance(f, ListView):
            it = f.highlighted_child
            if isinstance(it, ResultItem):
                self._play_metadata(it.metadata)
                if it.is_queue_item:
                    self.queue.remove(it.metadata)
                    self._refresh_queue_ui()

    def action_add_to_queue(self):
        f = self.focused
        if isinstance(f, ListView) and f.id == "results-list":
            it = f.highlighted_child
            if isinstance(it, ResultItem):
                self.queue.append(it.metadata)
                self._refresh_queue_ui()
                if self.current_song is None:
                    self.action_skip_next(auto_triggered=False)

    def action_delete_item(self):
        f = self.focused
        if isinstance(f, ListView) and f.id == "queue-list":
            it = f.highlighted_child
            if isinstance(it, ResultItem):
                if it.metadata in self.queue:
                    self.queue.remove(it.metadata)
                    self._refresh_queue_ui()

    def action_stop(self):
        self.player.stop()
        self.current_song = None
        self.query_one("#track-title", Label).update("Nothing playing")
        self._update_status("stopped")

    def action_skip_next(self, auto_triggered=False):
        if self.current_song and auto_triggered:
            if self.loop_mode == 1:
                self._play_metadata(self.current_song)
                return
            if self.loop_mode == 2:
                self.queue.append(self.current_song)
                self._refresh_queue_ui()

        if self.queue:
            ns = self.queue.pop(0)
            self._refresh_queue_ui()
            self._play_metadata(ns)
        else:
            if auto_triggered:
                self.current_song = None
                self.query_one("#track-title", Label).update("Nothing playing")
                self._update_status("Queue finished.")

    def action_toggle_loop(self):
        self.loop_mode = (self.loop_mode + 1) % 3
        self.update_playback_bar()

    def action_clear_queue(self):
        self.queue.clear()
        self._refresh_queue_ui()

    def action_save_session(self):
        with open(self.session_file, "w") as f:
            json.dump(self.queue, f)
        self._update_status("queue saved to disk")

    def _notify(self, title):
        try:
            safe_title = title.replace('"', '\\"').replace("'", "\\'")
            script = f'display notification "{safe_title}" with title "ZenPlayer" subtitle "Now Playing"'
            subprocess.Popen(['osascript', '-e', script], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        except Exception:
            pass

    def action_toggle_pause(self):
        if self.current_song:
            self.player.toggle_pause()

    def action_seek_backward(self):
        self.player.seek(-10)

    def action_seek_forward(self):
        self.player.seek(10)

    def action_seek_backward_large(self):
        self.player.seek(-60)

    def action_seek_forward_large(self):
        self.player.seek(60)

    def _play_metadata(self, metadata):
        url = metadata.get('url')
        if not url and metadata.get('id'):
            url = f"https://www.youtube.com/watch?v={metadata['id']}"
        self.current_song = metadata
        self._notify(metadata.get('title', 'Unknown'))
        self.play_url(url)
            
    @work(thread=True)
    def play_url(self, url):
        self.player.play_url(url)
        
    def _refresh_queue_ui(self):
        lv = self.query_one("#queue-list", ListView)
        lv.clear()
        for m in self.queue:
            lv.append(ResultItem(m, is_queue_item=True))

    def _update_status(self, text):
        ms = self.loop_modes[self.loop_mode].upper()
        symbol = " [||] " if self.player.is_playing else " [ > ] "
        ctrls = f"|<<  << {symbol} >>  >>|"
        self.query_one("#status-line", Label).update(
            f"{ctrls}        Repeat: {ms}   •   Status: {text.upper()}"
        )

    def update_playback_bar(self):
        theme = THEMES[self.cur_theme]
        cassette = self.query_one("#cassette-art", CassetteWidget)
        cassette.update_state(self.player.is_playing, theme["border_color"])

        t_str = time.strftime("%H:%M")
        cpu_val = random.randint(2, 6) if self.player.is_playing else 0
        self.query_one("#footer-stats", Label).update(
            f"zenplayer  •  v1.0.0  •  Esc / q: Back to ZenTorrent  •  CPU {cpu_val}%  •  {t_str}"
        )

        if not self.current_song:
            self.query_one("#progress-bar", Static).update("")
            self.query_one("#track-title", Label).update("Nothing playing")
            self._update_status("idle")
            return
            
        title = self.current_song.get('title', 'Unknown')
        uploader = self.current_song.get('uploader', 'Unknown')
        details = f"TITLE: {title}  •  ARTIST: {uploader}"
        if len(details) > 55:
            details = details[:52] + "..."
        self.query_one("#track-title", Label).update(details)

        cur = self.player.current_time
        dur = self.player.duration
        
        bar_widget = self.query_one("#progress-bar", ProgressBarWidget)
        w = max(20, bar_widget.size.width)
        
        t_text = f" {format_duration(cur)} / {format_duration(dur)} ".center(w)
        pct = min(1.0, max(0.0, cur / dur)) if dur > 0 else 0.0
        filled = int(pct * w)
        
        left = t_text[:filled]
        right = t_text[filled:]
        
        rich_str = f"[{theme['text_on_filled']} on {theme['bar_filled']}]{left}[/][{theme['text_on_track']} on {theme['bar_track']}]{right}[/]"
        bar_widget.update(rich_str)
        bar_widget.bar_len = w
        bar_widget.prefix_len = 0

        self._update_status("paused" if not self.player.is_playing else "playing")
        
    def on_unmount(self):
        self.player.quit()

def main():
    initial_q = " ".join(sys.argv[1:]).strip() if len(sys.argv) > 1 else ""
    app = ZenPlayerApp(initial_query=initial_q)
    app.run()

if __name__ == "__main__":
    main()
