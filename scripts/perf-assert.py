#!/usr/bin/env python3
# 查询结果条数断言：perf-run.sh 的 q() 调用。
# 用法: perf-assert.py <响应体文件> <模式> <期望值>
# 模式: items(==) / items_ge / items_gt0 / len(数组长度==) / len_gt0 / data_len(包裹对象 data 长度==)
# 退出码 0=断言通过，1=断言失败；空结果未命中期望同样判失败。
import json
import sys

path, mode, exp = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path) as f:
    d = json.load(f)
if mode == "items":
    v = d["items"]; ok = v == int(exp); info = "items=%d 期望 ==%s" % (v, exp)
elif mode == "items_ge":
    v = d["items"]; ok = v >= int(exp); info = "items=%d 期望 >=%s" % (v, exp)
elif mode == "items_gt0":
    v = d["items"]; ok = v > 0; info = "items=%d 期望 >0" % v
elif mode == "len":
    v = len(d); ok = v == int(exp); info = "数组长度=%d 期望 ==%s" % (v, exp)
elif mode == "len_gt0":
    v = len(d); ok = v > 0; info = "数组长度=%d 期望 >0" % v
elif mode == "data_len":
    v = len(d["data"]); ok = v == int(exp); info = "data 长度=%d 期望 ==%s（items=%s）" % (v, exp, d.get("items"))
else:
    print("未知断言模式: " + mode); sys.exit(2)
print(info)
sys.exit(0 if ok else 1)
