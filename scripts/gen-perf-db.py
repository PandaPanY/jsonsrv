#!/usr/bin/env python3
"""生成性能测试数据：10 万 posts + 2 万 comments。用法：gen-perf-db.py <输出文件>"""
import json
import sys

posts = [{"id": i, "title": "post %d" % i, "body": "body of post %d" % i,
          "author": {"name": "user%d" % (i % 1000), "email": "u%d@example.com" % (i % 1000)},
          "views": i % 100000} for i in range(1, 100001)]
comments = [{"id": i, "postId": i % 100000 + 1, "body": "comment %d" % i} for i in range(1, 20001)]
with open(sys.argv[1], "w") as f:
    json.dump({"posts": posts, "comments": comments}, f, separators=(",", ":"))
