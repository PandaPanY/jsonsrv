#!/usr/bin/env python3
"""并发 POST 探针：用法 concurrency-probe.py <base> <数量> <JSON 体>。

每个线程 POST 一次，统计状态码；完成后 GET 集合，统计记录数与唯一 id 数。
"""
import collections
import json
import sys
import threading
import urllib.error
import urllib.request

base, n, body = sys.argv[1], int(sys.argv[2]), sys.argv[3].encode()
codes = []
lock = threading.Lock()


def post():
    req = urllib.request.Request(base + "/posts", data=body, method="POST",
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            code = r.status
    except urllib.error.HTTPError as e:
            code = e.code
    with lock:
        codes.append(code)


threads = [threading.Thread(target=post) for _ in range(n)]
for t in threads:
    t.start()
for t in threads:
    t.join()

print("  HTTP 状态码:", dict(sorted(collections.Counter(codes).items())))
with urllib.request.urlopen(base + "/posts", timeout=60) as r:
    posts = json.load(r)
ids = [str(p.get("id")) for p in posts]
print("  GET /posts：记录数=%d 唯一 id 数=%d" % (len(ids), len(set(ids))))
