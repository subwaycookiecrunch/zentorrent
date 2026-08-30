import subprocess
import socket
import json
import os
import time
import threading

class ZenPlayerBackend:
    def __init__(self, socket_path='/tmp/zenplayer_mpv.sock', on_track_end=None):
        self.socket_path = socket_path
        self.process = None
        self._listener_thread = None
        self.on_track_end = on_track_end
        self.is_playing = False
        self.current_time = 0
        self.duration = 0
        
        if os.path.exists(self.socket_path):
            try:
                os.remove(self.socket_path)
            except OSError:
                pass

    def start(self):
        env = os.environ.copy()
        venv_bin = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'venv', 'bin')
        if os.path.exists(venv_bin):
            env['PATH'] = f"{venv_bin}:{env.get('PATH', '')}"

        cmd = [
            'mpv',
            '--idle',
            '--no-video',
            '--ytdl-format=bestaudio/best',
            f'--input-ipc-server={self.socket_path}',
            '--msg-level=all=no'
        ]
        self.process = subprocess.Popen(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env)
        time.sleep(0.3)
        
        self._listener_thread = threading.Thread(target=self._listen, daemon=True)
        self._listener_thread.start()
        
    def _send_command(self, cmd, retries=5):
        for _ in range(retries):
            try:
                client = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                client.connect(self.socket_path)
                client.sendall((json.dumps({"command": cmd}) + "\n").encode('utf-8'))
                client.close()
                return
            except Exception:
                time.sleep(0.1)

    def play_url(self, url):
        self._send_command(["loadfile", url])
        self.is_playing = True
        self.current_time = 0
        self.duration = 0

    def toggle_pause(self):
        self._send_command(["cycle", "pause"])
        self.is_playing = not self.is_playing

    def stop(self):
        self._send_command(["stop"])
        self.is_playing = False

    def seek(self, seconds, relative=True):
        self._send_command(["seek", seconds, "relative" if relative else "absolute"])

    def seek_percent(self, percent):
        self._send_command(["seek", percent, "absolute-percent"])

    def quit(self):
        try:
            self._send_command(["quit"])
        except Exception:
            pass
        if self.process:
            try:
                self.process.terminate()
                self.process.kill()
            except Exception:
                pass
        if os.path.exists(self.socket_path):
            try:
                os.remove(self.socket_path)
            except OSError:
                pass

    def _listen(self):
        client = None
        for _ in range(25):
            try:
                client = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                client.connect(self.socket_path)
                break
            except Exception:
                time.sleep(0.1)
        if not client:
            return

        try:
            client.sendall((json.dumps({"command": ["observe_property", 1, "time-pos"]}) + "\n").encode('utf-8'))
            client.sendall((json.dumps({"command": ["observe_property", 2, "duration"]}) + "\n").encode('utf-8'))
            
            buf = ""
            while True:
                data = client.recv(1024)
                if not data:
                    break
                buf += data.decode('utf-8')
                while '\n' in buf:
                    line, buf = buf.split('\n', 1)
                    if not line.strip():
                        continue
                    try:
                        ev = json.loads(line)
                        if ev.get('event') == 'property-change':
                            name = ev.get('name')
                            val = ev.get('data')
                            if val is not None:
                                if name == 'time-pos':
                                    self.current_time = float(val)
                                elif name == 'duration':
                                    self.duration = float(val)
                        elif ev.get('event') == 'end-file' and ev.get('reason') == 'eof':
                            if self.on_track_end:
                                self.on_track_end()
                    except json.JSONDecodeError:
                        pass
        except Exception:
            pass
