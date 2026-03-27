import json

try:
    import redis as redis_py
except Exception:
    redis_py = None

from mini_redis import MiniRedis


class RedisCompat:
    def __init__(self, host="127.0.0.1", port=6379, password="", db=0, timeout=30):
        self.host = host
        self.port = int(port)
        self.password = password or ""
        self.db = int(db or 0)
        self.timeout = timeout
        self.mode = "mini"
        self.sender = None
        self.receiver = None
        self.pubsub = None

    def connect(self):
        if redis_py is not None:
            try:
                client = redis_py.Redis(
                    host=self.host,
                    port=self.port,
                    password=self.password or None,
                    db=self.db,
                    decode_responses=True,
                    socket_timeout=self.timeout if self.timeout else None,
                )
                client.ping()
                self.mode = "redis"
                self.sender = client
                self.receiver = client
                self.pubsub = client.pubsub(ignore_subscribe_messages=True)
                return self
            except Exception:
                self.mode = "mini"
        self.sender = MiniRedis(self.host, self.port, self.password, self.db, self.timeout).connect()
        self.receiver = MiniRedis(self.host, self.port, self.password, self.db, self.timeout).connect()
        return self

    def publish(self, channel, payload):
        if self.mode == "redis":
            return self.sender.publish(channel, payload)
        return self.sender.publish(channel, payload)

    def subscribe(self, *channels):
        if self.mode == "redis":
            self.pubsub.subscribe(*channels)
            return None
        out = None
        for ch in channels:
            out = self.receiver.subscribe(ch)
        return out

    def read_message(self, timeout=1.0):
        if self.mode == "redis":
            msg = self.pubsub.get_message(timeout=timeout)
            if not msg:
                return None
            data = msg.get("data")
            if isinstance(data, (dict, list)):
                data = json.dumps(data, ensure_ascii=False)
            return {"channel": msg.get("channel"), "data": data}
        return self.receiver.read_pubsub_message()
