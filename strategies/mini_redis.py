import socket


class MiniRedis:
    def __init__(self, host="127.0.0.1", port=6379, password="", db=0, timeout=5):
        self.host = host
        self.port = int(port)
        self.password = password or ""
        self.db = int(db or 0)
        self.timeout = timeout
        self.sock = None
        self.buf = b""

    def connect(self):
        self.sock = socket.create_connection((self.host, self.port), timeout=self.timeout)
        self.sock.settimeout(self.timeout)
        if self.password:
            self.execute("AUTH", self.password)
        if self.db:
            self.execute("SELECT", str(self.db))
        return self

    def close(self):
        try:
            if self.sock:
                self.sock.close()
        finally:
            self.sock = None
            self.buf = b""

    def _encode(self, *parts):
        out = [f"*{len(parts)}\r\n".encode("utf-8")]
        for p in parts:
            if p is None:
                p = ""
            if not isinstance(p, (bytes, bytearray)):
                p = str(p).encode("utf-8")
            out.append(f"${len(p)}\r\n".encode("utf-8"))
            out.append(p)
            out.append(b"\r\n")
        return b"".join(out)

    def _read_exact(self, n):
        while len(self.buf) < n:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise ConnectionError("redis connection closed")
            self.buf += chunk
        out, self.buf = self.buf[:n], self.buf[n:]
        return out

    def _read_line(self):
        while b"\r\n" not in self.buf:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise ConnectionError("redis connection closed")
            self.buf += chunk
        i = self.buf.index(b"\r\n")
        line, self.buf = self.buf[:i], self.buf[i + 2 :]
        return line

    def _read_resp(self):
        prefix = self._read_exact(1)
        if prefix == b"+":
            return self._read_line().decode("utf-8", errors="replace")
        if prefix == b"-":
            raise RuntimeError(self._read_line().decode("utf-8", errors="replace"))
        if prefix == b":":
            return int(self._read_line())
        if prefix == b"$":
            n = int(self._read_line())
            if n == -1:
                return None
            data = self._read_exact(n)
            _ = self._read_exact(2)
            return data.decode("utf-8", errors="replace")
        if prefix == b"*":
            n = int(self._read_line())
            if n == -1:
                return None
            return [self._read_resp() for _ in range(n)]
        raise RuntimeError(f"unknown RESP prefix: {prefix!r}")

    def execute(self, *args):
        if not self.sock:
            self.connect()
        self.sock.sendall(self._encode(*args))
        return self._read_resp()

    def publish(self, channel, payload):
        return self.execute("PUBLISH", channel, payload)

    def subscribe(self, channel):
        return self.execute("SUBSCRIBE", channel)

    def read_pubsub_message(self):
        msg = self._read_resp()
        if not isinstance(msg, list) or len(msg) < 3:
            return None
        if msg[0] != "message":
            return None
        return {"channel": msg[1], "data": msg[2]}

