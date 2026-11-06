#!/usr/bin/env python3
"""对照测试：把 JsonSrv 和真实的 json-server 放在一起跑同一批请求，逐字段比较。

自管生命周期：按"组"重置数据并重启两侧的 参考版 json-server 与 JsonSrv，
逐条执行请求清单，比较状态码、关键响应头、响应体、写后的资源 GET 结果
和数据文件内容；GET 自动补发 HEAD 并比较。

归一化规则（NORM_RULES，输出时列出理由）：
  1. 随机 id：仅 id 字段的随机短串值替换为 <ID>。
  2. id/外键类型：id 与以 Id 结尾的外键字段，数字与同值字符串等价
     （v1 beta.15 读库字符串化 id；JsonSrv 保留原类型）。
  3. URL：端口剥离、查询参数排序（Link/envelope 里的 URL）。
  4. 响应体按解析后的 JSON 语义比较（忽略键顺序与文本格式）。
  5. 错误体（状态码一致且 >=400）：文本内容不参与比较（措辞不同）。

登记表 KNOWN：逐字段登记（plan/请求/字段/原版值/JsonSrv 值/理由）。
  登记值写死精确字符串；登记与实际不符或登记未出现都算 FAIL。

用法:
  compare.py --plan v1|017|default017|extrav1|extra017|default017b|legacy017b|v1b --requests FILE [--routes FILE]
             --bin <jsonsrv> --refbin <json-server>
             --db <compare-db.json> [--ref-port 3021] [--impl-port 3022]
             [--impl-flags "--legacy"] [--out FILE]
"""

import argparse
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from urllib.parse import quote, urlparse, urlunparse, urlencode, parse_qsl

ORIGIN = "http://localhost:5173"

# 参与比较的响应头
CMP_HEADERS = [
    "content-type", "x-total-count", "link", "content-encoding",
    "access-control-allow-origin", "access-control-allow-credentials",
    "access-control-expose-headers", "vary",
]

# 随机短 id（nanoid 风格，字母表含 - 与 _）；仅作用于 id 字段值
RANDOM_ID = re.compile(r"(?=[A-Za-z0-9_-]*[0-9A-Z])[A-Za-z0-9_-]{7,11}")

NORM_RULES = [
    ("id 字段随机值", "两侧各自随机生成（实测：原版 v0.17 为 nanoid 7 位、v1 beta.15 为 11 位，JsonSrv 为 11 位），统一替换为 <ID>；兼容文档豁免清单"),
    ("id/外键字段类型", "v1 beta.15 读库时把数字 id 字符串化、外键比较用严格相等；JsonSrv 保留原类型并做数值等价匹配；见兼容文档 D1"),
    ("URL 形式", "Link/包裹对象中的 URL 剥离端口、查询参数排序后比较"),
    ("响应 JSON 文本格式", "原版 2 空格缩进、JsonSrv 紧凑单行，按解析后的 JSON 语义比较（键顺序不参与）"),
    ("错误响应体文本", "状态码一致且 >=400 时不比较错误文本（两边措辞不同）"),
    ("数据文件 $schema 字段", "原版 v1 写库后向数据文件写入 $schema 提示字段，JsonSrv 不写；属文件形态差异、不影响 API 行为，比较数据文件时剥离该字段"),
    ("大整数精度回环", "JSON number 走 IEEE754 float64：>2^53 整数原版落盘必然丢精度（实测 12345678901234567890 回写为 12345678901234567000），JsonSrv 保数据文件原文；两侧按 float64 回环视为相等；见兼容文档 D12"),
]

# 逐字段有意差异登记。字段名：status / header.<名> / body.<jsonpath> /
# head.status / head.header.<名> / verify:<METHOD path>.<jsonpath> / file.<jsonpath>
# 值写死精确字符串；"@HTML" 表示双方都是 HTML 首页。
KNOWN = [
    {"plan":"v1","req":"（规则）","field":"@rule:header.content-type","ref":"\"application/json\"","impl":"\"application/json; charset=utf-8\"","reason":"D9: v1 beta.15 JSON 响应不带 charset；JsonSrv 始终带 charset=utf-8（按 0.17 惯例），HTTP 语义等价；见兼容文档 D9"},
    {"plan":"v1","req":"（规则）","field":"@rule:head.header.content-type","ref":"\"application/json\"","impl":"\"application/json; charset=utf-8\"","reason":"D9: v1 beta.15 JSON 响应不带 charset；JsonSrv 始终带 charset=utf-8（按 0.17 惯例），HTTP 语义等价；见兼容文档 D9"},
    {"plan":"v1","req":"（规则）","field":"@rule:header.access-control-allow-origin","ref":"\"*\"","impl":"\"http://localhost:5173\"","reason":"D6: CORS 回显：JsonSrv 带 Origin 时回显该 Origin（v1 beta.15 固定 *），便于前端带 Cookie 联调；见兼容文档 D6"},
    {"plan":"v1","req":"（规则）","field":"@rule:head.header.access-control-allow-origin","ref":"\"*\"","impl":"\"http://localhost:5173\"","reason":"D6: CORS 回显：JsonSrv 带 Origin 时回显该 Origin（v1 beta.15 固定 *），便于前端带 Cookie 联调；见兼容文档 D6"},
    {"plan":"v1","req":"（规则）","field":"@rule:header.access-control-allow-credentials","ref":"null","impl":"\"true\"","reason":"D6: JsonSrv 回显 Access-Control-Allow-Credentials: true（v1 beta.15 不发该头）；见兼容文档 D6"},
    {"plan":"v1","req":"（规则）","field":"@rule:head.header.access-control-allow-credentials","ref":"null","impl":"\"true\"","reason":"D6: JsonSrv 回显 Access-Control-Allow-Credentials: true（v1 beta.15 不发该头）；见兼容文档 D6"},
    {"plan":"v1","req":"（规则）","field":"@rule:header.vary","ref":"null","impl":"\"Origin, Accept-Encoding\"","reason":"D7: JsonSrv 输出 Vary: Origin, Accept-Encoding（v1 beta.15 实测不发 Vary；与 0.17 对齐）；见兼容文档 D7"},
    {"plan":"v1","req":"（规则）","field":"@rule:head.header.vary","ref":"null","impl":"\"Origin, Accept-Encoding\"","reason":"D7: JsonSrv 输出 Vary: Origin, Accept-Encoding（v1 beta.15 实测不发 Vary；与 0.17 对齐）；见兼容文档 D7"},
    {"plan":"017","req":"（规则）","field":"@rule:header.content-type","ref":"\"text/html; charset=UTF-8\"","impl":"\"text/html; charset=utf-8\"","reason":"D8/D9: 首页 HTML 的 Content-Type charset 大小写差异（0.17 大写 UTF-8，Go net/http 固定小写 utf-8），HTTP 语义等价；见兼容文档 D8、D9"},
    {"plan":"017","req":"（规则）","field":"@rule:head.header.content-type","ref":"\"text/html; charset=UTF-8\"","impl":"\"text/html; charset=utf-8\"","reason":"D8/D9: 首页 HTML 的 Content-Type charset 大小写差异（0.17 大写 UTF-8，Go net/http 固定小写 utf-8），HTTP 语义等价；见兼容文档 D8、D9"},
    {"plan":"v1","req":"GET /posts?_embed=comments","field":"body[0].comments.len","ref":"len:0","impl":"len:3","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /posts?_embed=comments","field":"body[1].comments.len","ref":"len:0","impl":"len:3","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /posts?_embed=comments","field":"body[2].comments.len","ref":"len:0","impl":"len:3","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /comments?_embed=post","field":"body[0].post","ref":"\"<missing>\"","impl":"{\"author\":{\"name\":\"bob\"},\"id\":2,\"tags\":[\"b\",\"c\"],\"title\":\"Post 2\",\"views\":20}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /comments?_embed=post","field":"body[1].post","ref":"\"<missing>\"","impl":"{\"author\":{\"name\":\"alice\"},\"id\":3,\"tags\":[\"a\"],\"title\":\"Post 3\",\"views\":30}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /comments?_embed=post","field":"body[2].post","ref":"\"<missing>\"","impl":"{\"author\":{\"name\":\"alice\"},\"id\":1,\"tags\":[\"a\"],\"title\":\"Post 1\",\"views\":10}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /comments?_embed=post","field":"body[3].post","ref":"\"<missing>\"","impl":"{\"author\":{\"name\":\"bob\"},\"id\":2,\"tags\":[\"b\",\"c\"],\"title\":\"Post 2\",\"views\":20}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /comments?_embed=post","field":"body[4].post","ref":"\"<missing>\"","impl":"{\"author\":{\"name\":\"alice\"},\"id\":3,\"tags\":[\"a\"],\"title\":\"Post 3\",\"views\":30}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /comments?_embed=post","field":"body[5].post","ref":"\"<missing>\"","impl":"{\"author\":{\"name\":\"alice\"},\"id\":1,\"tags\":[\"a\"],\"title\":\"Post 1\",\"views\":10}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /comments?_embed=post","field":"body[6].post","ref":"\"<missing>\"","impl":"{\"author\":{\"name\":\"bob\"},\"id\":2,\"tags\":[\"b\",\"c\"],\"title\":\"Post 2\",\"views\":20}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /comments?_embed=post","field":"body[7].post","ref":"\"<missing>\"","impl":"{\"author\":{\"name\":\"alice\"},\"id\":3,\"tags\":[\"a\"],\"title\":\"Post 3\",\"views\":30}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /comments?_embed=post","field":"body[8].post","ref":"\"<missing>\"","impl":"{\"author\":{\"name\":\"alice\"},\"id\":1,\"tags\":[\"a\"],\"title\":\"Post 1\",\"views\":10}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /comments/1?_embed=post","field":"body.post","ref":"\"<missing>\"","impl":"{\"author\":{\"name\":\"bob\"},\"id\":2,\"tags\":[\"b\",\"c\"],\"title\":\"Post 2\",\"views\":20}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /posts/1?_embed=comments","field":"body.comments.len","ref":"len:0","impl":"len:3","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /articles/1?_embed=notes","field":"body.notes.len","ref":"len:0","impl":"len:1","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /movies/1?_embed=reviews","field":"body.reviews.len","ref":"len:0","impl":"len:1","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /images/1?_embed=thumbs","field":"body.thumbs.len","ref":"len:0","impl":"len:1","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /images/1?_embed=thumbs&_embed=comments","field":"body.thumbs.len","ref":"len:0","impl":"len:1","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /notes/1?_embed=article","field":"body.article","ref":"\"<missing>\"","impl":"{\"id\":1,\"title\":\"art one\"}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /people/1?_embed=address","field":"body.address","ref":"\"<missing>\"","impl":"{\"city\":\"x\",\"id\":1}","reason":"D1: v1 beta.15 读库把数字 id 字符串化、外键严格相等，_embed 匹配不到（字段缺失/空数组）；JsonSrv 保留原类型并按数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /posts?id:in=2,3,4","field":"body.len","ref":"len:0","impl":"len:3","reason":"D1: v1 beta.15 id 字符串化后 `id:in=2,3,4` 在数字库查不到（实测 0 命中）；JsonSrv 数值等价匹配；见兼容文档 D1"},
    {"plan":"v1","req":"GET /posts?id:ne=1","field":"body.len","ref":"len:25","impl":"len:24","reason":"D1: v1 beta.15 id 字符串化后 `id:ne=1` 实际无效果（实测全量 25 条）；JsonSrv 数值等价排除 id=1 得 24 条；按 id 序列一条登记；见兼容文档 D1"},
{"plan": "v1", "req": "GET /posts?id:ne=1", "field": "@idseq", "ref": "[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24]", "impl": "[2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25]", "reason": "D1: id 运算符序列（v1beta id 字符串化后 ne 排除失效，首条 id=1 保留在 ref；JsonSrv 按 D1 语义排除数字 id=1）"},
    {"plan":"v1","req":"GET /posts/1/comments","field":"body","ref":"{\"error\":\"Not Found\"}","impl":"[{\"body\":\"c3\",\"id\":3,\"postId\":1},{\"body\":\"c6\",\"id\":6,\"postId\":1},{\"body\":\"c9\",\"id\":9,\"postId\":1}]","reason":"D3: v1 不支持嵌套路由（实测 404 英文错误体），JsonSrv 按 0.17 支持；见兼容文档 D3"},
    {"plan":"v1","req":"GET /articles/1/notes","field":"body","ref":"{\"error\":\"Not Found\"}","impl":"[{\"articleId\":1,\"body\":\"n1\",\"id\":1}]","reason":"D3: v1 不支持嵌套路由（实测 404 英文错误体），JsonSrv 按 0.17 支持；见兼容文档 D3"},
    {"plan":"v1","req":"GET /movies/1/reviews","field":"body","ref":"{\"error\":\"Not Found\"}","impl":"[{\"id\":1,\"movieId\":1,\"text\":\"r1\"}]","reason":"D3: v1 不支持嵌套路由（实测 404 英文错误体），JsonSrv 按 0.17 支持；见兼容文档 D3"},
    {"plan":"v1","req":"GET /images/1/thumbs","field":"body","ref":"{\"error\":\"Not Found\"}","impl":"[{\"id\":1,\"imageId\":1}]","reason":"D3: v1 不支持嵌套路由（实测 404 英文错误体），JsonSrv 按 0.17 支持；见兼容文档 D3"},
    {"plan":"v1","req":"GET /db","field":"body.addresses","ref":"\"<missing>\"","impl":"[{\"city\":\"x\",\"id\":1}]","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.articles","ref":"\"<missing>\"","impl":"[{\"id\":1,\"title\":\"art one\"},{\"id\":2,\"title\":\"art two\"}]","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.comments","ref":"\"<missing>\"","impl":"[{\"body\":\"c1\",\"id\":1,\"postId\":2},{\"body\":\"c2\",\"id\":2,\"postId\":3},{\"body\":\"c3\",\"id\":3,\"postId\":1},{\"body\":\"c4\",\"id\":4,\"postId\":2},{\"body\":\"c5…","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.error","ref":"Not Found","impl":"<missing>","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.images","ref":"\"<missing>\"","impl":"[{\"id\":1,\"url\":\"u1\"}]","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.mixed","ref":"\"<missing>\"","impl":"[{\"id\":1,\"s\":\"apple\",\"v\":10},{\"id\":2,\"s\":\"Banana\",\"v\":null},{\"id\":3,\"s\":\"cherry\"},{\"id\":4,\"s\":\"apple\",\"v\":2}]","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.movies","ref":"\"<missing>\"","impl":"[{\"id\":1,\"name\":\"m1\"}]","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.notes","ref":"\"<missing>\"","impl":"[{\"articleId\":1,\"body\":\"n1\",\"id\":1},{\"articleId\":2,\"body\":\"n2\",\"id\":2}]","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.people","ref":"\"<missing>\"","impl":"[{\"addressId\":1,\"id\":1,\"name\":\"Pat\"}]","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.posts","ref":"\"<missing>\"","impl":"[{\"author\":{\"name\":\"alice\"},\"id\":1,\"tags\":[\"a\"],\"title\":\"Post 1\",\"views\":10},{\"author\":{\"name\":\"bob\"},\"id\":2,\"tags\":[\"b\",\"c\"],\"title\":\"Post …","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.profile","ref":"\"<missing>\"","impl":"{\"name\":\"typicode\"}","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.reviews","ref":"\"<missing>\"","impl":"[{\"id\":1,\"movieId\":1,\"text\":\"r1\"}]","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"body.thumbs","ref":"\"<missing>\"","impl":"[{\"id\":1,\"imageId\":1}]","reason":"D2: v1 beta.15 不支持 GET /db（404 英文错误体），JsonSrv 支持全量读取（与 0.17 一致）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /","field":"body","ref":"@HTML","impl":"@HTML","reason":"D8: 内置 HTML 首页两侧都是精简 HTML，页面内容允许不同（语义等价 @HTML）；见兼容文档 D8"},
    {"plan":"017","req":"GET /","field":"body","ref":"@HTML","impl":"@HTML","reason":"D8: 内置 HTML 首页两侧都是精简 HTML，页面内容允许不同（语义等价 @HTML）；见兼容文档 D8"},
    {"plan":"v1","req":"POST /posts","field":"body.message","ref":"<missing>","impl":"id \"2\" already exists","reason":"D4: v1 beta.15 不查重 POST 显式重复 id（实测 201 追加）；JsonSrv 返回 409 冲突拒绝写入；见兼容文档 D4"},
    {"plan":"v1","req":"POST /posts","field":"body.title","ref":"dup id","impl":"<missing>","reason":"D4: v1 beta.15 不查重 POST 显式重复 id（实测 201 追加）；JsonSrv 返回 409 冲突拒绝写入；见兼容文档 D4"},
    {"plan":"default017","req":"GET /books?_sort=title&_order=desc","field":"@idseq","ref":"[2,5,3]","impl":"[5,3,2]","reason":"D10: 排序比较语义按计划不同：默认模式沿用 v1 的忽略大小写归一序，0.17 是 lodash 码位序，结果顺序不同；按 id 序列一条登记"},
    {"plan":"v1","req":"GET /posts?_start=0&_end=2","field":"body.len","ref":"len:0","impl":"len:2","reason":"D10: 默认模式识别 v0.17 切片写法 _start/_end/_limit（v1 beta.15 不识别、按字段过滤返回空数组）；JsonSrv 按 0.17 切片语义（_end 排他、负索引）返回窗口+X-Total-Count；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_limit=3","field":"body.len","ref":"len:0","impl":"len:3","reason":"D10: 默认模式识别 v0.17 切片写法 _start/_end/_limit（v1 beta.15 不识别、按字段过滤返回空数组）；JsonSrv 按 0.17 切片语义（_end 排他、负索引）返回窗口+X-Total-Count；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=20&_end=30","field":"body.len","ref":"len:0","impl":"len:5","reason":"D10: 默认模式识别 v0.17 切片写法 _start/_end/_limit（v1 beta.15 不识别、按字段过滤返回空数组）；JsonSrv 按 0.17 切片语义（_end 排他、负索引）返回窗口+X-Total-Count；见兼容文档 D10"},
{"plan": "extrav1", "req": "（规则）", "field": "@rule:header.content-type", "ref": "\"application/json\"", "impl": "\"application/json; charset=utf-8\"", "reason": "D9: JSON 响应 Content-Type 带 charset=utf-8（RFC 8259），HEAD 回显同理（见兼容文档 D9）"},
{"plan": "extrav1", "req": "（规则）", "field": "@rule:head.header.content-type", "ref": "\"application/json\"", "impl": "\"application/json; charset=utf-8\"", "reason": "D9: HEAD 回显 JSON 响应头 Content-Type 带 charset（见兼容文档 D9）"},
{"plan": "extrav1", "req": "（规则）", "field": "@rule:header.access-control-allow-origin", "ref": "\"*\"", "impl": "\"http://localhost:5173\"", "reason": "D6: 原版 ACAO 固定 *，JsonSrv 回显请求 Origin（带凭据跨域可用，见兼容文档 D6）"},
{"plan": "extrav1", "req": "（规则）", "field": "@rule:head.header.access-control-allow-origin", "ref": "\"*\"", "impl": "\"http://localhost:5173\"", "reason": "D6: HEAD 回显同上（见兼容文档 D6）"},
{"plan": "extrav1", "req": "（规则）", "field": "@rule:header.access-control-allow-credentials", "ref": "null", "impl": "\"true\"", "reason": "D6: 原版不声明凭据，JsonSrv 回显 Access-Control-Allow-Credentials: true（见兼容文档 D6）"},
{"plan": "extrav1", "req": "（规则）", "field": "@rule:head.header.access-control-allow-credentials", "ref": "null", "impl": "\"true\"", "reason": "D6: HEAD 回显同上（见兼容文档 D6）"},
{"plan": "extrav1", "req": "（规则）", "field": "@rule:header.vary", "ref": "null", "impl": "\"Origin, Accept-Encoding\"", "reason": "D7: 原版无 Vary，JsonSrv 输出 Vary: Origin, Accept-Encoding（缓存正确性，见兼容文档 D7）"},
{"plan": "extrav1", "req": "（规则）", "field": "@rule:head.header.vary", "ref": "null", "impl": "\"Origin, Accept-Encoding\"", "reason": "D7: HEAD 回显同上（见兼容文档 D7）"},
{"plan": "extrav1", "req": "GET /books?id=1", "field": "body.len", "ref": "len:0", "impl": "len:1", "reason": "D1: v1beta id 字符串化后数字等值 id=1 命中 0 条；JsonSrv 命中 1 条（见兼容文档 D1）"},
{"plan": "extrav1", "req": "GET /books?_page=2&_per_page=0", "field": "@idseq", "ref": "[2]", "impl": "[1]", "reason": "D11: 原版把 _per_page=0 当页大小 0（pages=5、第 2 页只剩 1 条）；JsonSrv 把非法 0 按默认 10 处理，整库一页（见兼容文档 D11）"},
{"plan": "extrav1", "req": "GET /books?_page=abc", "field": "body", "ref": "[{\"authorId\":\"a1\",\"categoryId\":1,\"id\":1,\"inStock\":true,\"labels\":[\"x\",\"y\"],\"meta\":{\"lang\":\"en\",\"pages\":300},\"price\":10,\"rating\":4.5,\"title\":\"…", "impl": "{\"data\":[{\"authorId\":\"a1\",\"categoryId\":1,\"id\":1,\"inStock\":true,\"labels\":[\"x\",\"y\"],\"meta\":{\"lang\":\"en\",\"pages\":300},\"price\":10,\"rating\":4.5,\"…", "reason": "D11: 原版非法 _page 时返回裸数组；JsonSrv 按 v1 包裹对象语义、页号按 1 处理（见兼容文档 D11）"},
{"plan": "extrav1", "req": "POST /db", "field": "verify:/db.authors", "ref": "\"<missing>\"", "impl": "[{\"id\":\"a1\",\"name\":\"Ann\"},{\"id\":\"a2\",\"name\":\"Bob\"},{\"id\":\"a3\",\"name\":\"Cy\"}]", "reason": "D2: v1beta 不支持 GET /db（读回 404）；JsonSrv 支持读回整库（见兼容文档 D2）"},
{"plan": "extrav1", "req": "POST /db", "field": "verify:/db.books", "ref": "\"<missing>\"", "impl": "[{\"authorId\":\"a1\",\"categoryId\":1,\"id\":1,\"inStock\":true,\"labels\":[\"x\",\"y\"],\"meta\":{\"lang\":\"en\",\"pages\":300},\"price\":10,\"rating\":4.5,\"title\":\"…", "reason": "D2: v1beta 不支持 GET /db（读回 404）；JsonSrv 支持读回整库（见兼容文档 D2）"},
{"plan": "extrav1", "req": "POST /db", "field": "verify:/db.boxes", "ref": "\"<missing>\"", "impl": "[{\"id\":1,\"w\":1}]", "reason": "D2: v1beta 不支持 GET /db（读回 404）；JsonSrv 支持读回整库（见兼容文档 D2）"},
{"plan": "extrav1", "req": "POST /db", "field": "verify:/db.categories", "ref": "\"<missing>\"", "impl": "[{\"id\":1,\"name\":\"lang\"},{\"id\":2,\"name\":\"tool\"}]", "reason": "D2: v1beta 不支持 GET /db（读回 404）；JsonSrv 支持读回整库（见兼容文档 D2）"},
{"plan": "extrav1", "req": "POST /db", "field": "verify:/db.empties", "ref": "\"<missing>\"", "impl": "[]", "reason": "D2: v1beta 不支持 GET /db（读回 404）；JsonSrv 支持读回整库（见兼容文档 D2）"},
{"plan": "extrav1", "req": "POST /db", "field": "verify:/db.error", "ref": "Not Found", "impl": "<missing>", "reason": "D2: v1beta 不支持 GET /db（读回 404）；JsonSrv 支持读回整库（见兼容文档 D2）"},
{"plan": "extrav1", "req": "POST /db", "field": "verify:/db.items", "ref": "\"<missing>\"", "impl": "[{\"boxId\":1,\"id\":1}]", "reason": "D2: v1beta 不支持 GET /db（读回 404）；JsonSrv 支持读回整库（见兼容文档 D2）"},
{"plan": "extrav1", "req": "POST /db", "field": "verify:/db.logs", "ref": "\"<missing>\"", "impl": "[{\"id\":1,\"statusId\":1}]", "reason": "D2: v1beta 不支持 GET /db（读回 404）；JsonSrv 支持读回整库（见兼容文档 D2）"},
{"plan": "extrav1", "req": "POST /db", "field": "verify:/db.settings", "ref": "\"<missing>\"", "impl": "{\"theme\":\"x\"}", "reason": "D2: v1beta 不支持 GET /db（读回 404）；JsonSrv 支持读回整库（见兼容文档 D2）"},
{"plan": "extrav1", "req": "POST /db", "field": "verify:/db.statuses", "ref": "\"<missing>\"", "impl": "[{\"id\":1,\"s\":\"on\"}]", "reason": "D2: v1beta 不支持 GET /db（读回 404）；JsonSrv 支持读回整库（见兼容文档 D2）"},
    {"plan":"v1","req":"GET /posts?_start=-3&_limit=2","field":"body.len","ref":"len:0","impl":"len:2","reason":"D10: 默认模式识别 v0.17 切片写法 _start/_end/_limit（v1 beta.15 不识别、按字段过滤返回空数组）；JsonSrv 按 0.17 切片语义（_end 排他、负索引）返回窗口+X-Total-Count；见兼容文档 D10"},
    {"plan":"v1b","req":"（规则）","field":"@rule:header.content-type","ref":"\"application/json\"","impl":"\"application/json; charset=utf-8\"","reason":"D9: v1 beta.15 JSON 响应不带 charset；JsonSrv 始终带 charset=utf-8（按 0.17 惯例），HTTP 语义等价；见兼容文档 D9"},
    {"plan":"v1b","req":"（规则）","field":"@rule:head.header.content-type","ref":"\"application/json\"","impl":"\"application/json; charset=utf-8\"","reason":"D9: v1 beta.15 JSON 响应不带 charset；JsonSrv 始终带 charset=utf-8（按 0.17 惯例），HTTP 语义等价；见兼容文档 D9"},
    {"plan":"v1b","req":"（规则）","field":"@rule:header.access-control-allow-origin","ref":"\"*\"","impl":"\"http://localhost:5173\"","reason":"D6: CORS 回显：JsonSrv 带 Origin 时回显该 Origin（v1 beta.15 固定 *），便于前端带 Cookie 联调；见兼容文档 D6"},
    {"plan":"v1b","req":"（规则）","field":"@rule:head.header.access-control-allow-origin","ref":"\"*\"","impl":"\"http://localhost:5173\"","reason":"D6: CORS 回显：JsonSrv 带 Origin 时回显该 Origin（v1 beta.15 固定 *），便于前端带 Cookie 联调；见兼容文档 D6"},
    {"plan":"v1b","req":"（规则）","field":"@rule:header.access-control-allow-credentials","ref":"null","impl":"\"true\"","reason":"D6: JsonSrv 回显 Access-Control-Allow-Credentials: true（v1 beta.15 不发该头）；见兼容文档 D6"},
    {"plan":"v1b","req":"（规则）","field":"@rule:head.header.access-control-allow-credentials","ref":"null","impl":"\"true\"","reason":"D6: JsonSrv 回显 Access-Control-Allow-Credentials: true（v1 beta.15 不发该头）；见兼容文档 D6"},
    {"plan":"v1b","req":"（规则）","field":"@rule:header.vary","ref":"null","impl":"\"Origin, Accept-Encoding\"","reason":"D7: JsonSrv 输出 Vary: Origin, Accept-Encoding（v1 beta.15 实测不发 Vary；与 0.17 对齐）；见兼容文档 D7"},
    {"plan":"v1b","req":"（规则）","field":"@rule:head.header.vary","ref":"null","impl":"\"Origin, Accept-Encoding\"","reason":"D7: JsonSrv 输出 Vary: Origin, Accept-Encoding（v1 beta.15 实测不发 Vary；与 0.17 对齐）；见兼容文档 D7"},
    {"plan":"v1","req":"GET /posts?q=post","field":"body.len","ref":"len:0","impl":"len:25","reason":"D5: v1 beta.15 实测 q 全文检索对 compare-db 落空（JsonSrv 按 v1 语义实现全文匹配）；见兼容文档 D5"},
    {"plan":"v1","req":"GET /posts?q=Post","field":"body.len","ref":"len:0","impl":"len:25","reason":"D5: v1 beta.15 实测 q 全文检索对 compare-db 落空（JsonSrv 按 v1 语义实现全文匹配）；见兼容文档 D5"},
    {"plan":"v1","req":"GET /posts/1/comments","field":"status","ref":"404","impl":"200","reason":"D3: v1 beta.15 不支持嵌套路由（实测 404），JsonSrv 支持父资源嵌套子集合（兼容文档对照表）；见兼容文档 D3"},
    {"plan":"v1b","req":"GET /products?sku:eq=007","field":"body.len","ref":"len:0","impl":"len:1","reason":"D13+D1: v1 beta.15 strict :eq match, param converted to number unequal to string sku 007, returns empty; JsonSrv relaxes string equality (see 兼容文档 D13, D1)"},
    {"plan":"v1b","req":"GET /products?sku:in=007,9","field":"body.len","ref":"len:0","impl":"len:2","reason":"D13+D1: same as sku:eq; :in list params all converted to numbers unequal to string sku, returns empty; JsonSrv relaxes matches 007 and 9 (see 兼容文档 D13, D1)"},
    {"plan":"v1b","req":"GET /products?dims.w:eq=2","field":"body.len","ref":"len:3","impl":"len:2","reason":"D13: v1 beta.15 colon nested defect, products without dims (id5) wrongly matched by :eq; JsonSrv treats nested-field missing as no match, see 兼容文档 D13"},
    {"plan":"extrav1","req":"POST /db","field":"verify:/db.nums","ref":"\"<missing>\"","impl":"[{\"big\":1.2345678901234567e+19,\"e\":1000.0,\"f\":1.1,\"id\":1}]","reason":"D2: v1 beta.15 not support POST /db, verify GET /db missing nums key; JsonSrv supports writing and reading back the whole DB (see 兼容文档 D2)"},
    {"plan":"v1","req":"GET /posts/1/comments","field":"head.status","ref":"404","impl":"200","reason":"D3: v1 beta.15 不支持嵌套路由（实测 404），JsonSrv 支持父资源嵌套子集合（兼容文档对照表）；见兼容文档 D3"},
    {"plan":"v1","req":"GET /articles/1/notes","field":"status","ref":"404","impl":"200","reason":"D3: v1 beta.15 不支持嵌套路由（实测 404），JsonSrv 支持父资源嵌套子集合（兼容文档对照表）；见兼容文档 D3"},
    {"plan":"v1","req":"GET /articles/1/notes","field":"head.status","ref":"404","impl":"200","reason":"D3: v1 beta.15 不支持嵌套路由（实测 404），JsonSrv 支持父资源嵌套子集合（兼容文档对照表）；见兼容文档 D3"},
    {"plan":"v1","req":"GET /movies/1/reviews","field":"status","ref":"404","impl":"200","reason":"D3: v1 beta.15 不支持嵌套路由（实测 404），JsonSrv 支持父资源嵌套子集合（兼容文档对照表）；见兼容文档 D3"},
    {"plan":"v1","req":"GET /movies/1/reviews","field":"head.status","ref":"404","impl":"200","reason":"D3: v1 beta.15 不支持嵌套路由（实测 404），JsonSrv 支持父资源嵌套子集合（兼容文档对照表）；见兼容文档 D3"},
    {"plan":"v1","req":"GET /images/1/thumbs","field":"status","ref":"404","impl":"200","reason":"D3: v1 beta.15 不支持嵌套路由（实测 404），JsonSrv 支持父资源嵌套子集合（兼容文档对照表）；见兼容文档 D3"},
    {"plan":"v1","req":"GET /images/1/thumbs","field":"head.status","ref":"404","impl":"200","reason":"D3: v1 beta.15 不支持嵌套路由（实测 404），JsonSrv 支持父资源嵌套子集合（兼容文档对照表）；见兼容文档 D3"},
    {"plan":"v1","req":"GET /db","field":"status","ref":"404","impl":"200","reason":"D2: v1 beta.15 不提供 GET /db（实测 404），JsonSrv 返回整个数据库（兼容文档对照表）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /db","field":"head.status","ref":"404","impl":"200","reason":"D2: v1 beta.15 不提供 GET /db（实测 404），JsonSrv 返回整个数据库（兼容文档对照表）；见兼容文档 D2"},
    {"plan":"v1","req":"GET /posts?_start=0&_end=2","field":"header.x-total-count","ref":"null","impl":"\"25\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=0&_end=2","field":"header.access-control-expose-headers","ref":"null","impl":"\"X-Total-Count\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=0&_end=2","field":"head.header.x-total-count","ref":"null","impl":"\"25\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=0&_end=2","field":"head.header.access-control-expose-headers","ref":"null","impl":"\"X-Total-Count\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_limit=3","field":"header.x-total-count","ref":"null","impl":"\"25\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_limit=3","field":"header.access-control-expose-headers","ref":"null","impl":"\"X-Total-Count\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_limit=3","field":"head.header.x-total-count","ref":"null","impl":"\"25\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_limit=3","field":"head.header.access-control-expose-headers","ref":"null","impl":"\"X-Total-Count\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=20&_end=30","field":"header.x-total-count","ref":"null","impl":"\"25\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=20&_end=30","field":"header.access-control-expose-headers","ref":"null","impl":"\"X-Total-Count\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=20&_end=30","field":"head.header.x-total-count","ref":"null","impl":"\"25\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=20&_end=30","field":"head.header.access-control-expose-headers","ref":"null","impl":"\"X-Total-Count\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=-3&_limit=2","field":"header.x-total-count","ref":"null","impl":"\"25\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=-3&_limit=2","field":"header.access-control-expose-headers","ref":"null","impl":"\"X-Total-Count\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=-3&_limit=2","field":"head.header.x-total-count","ref":"null","impl":"\"25\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_start=-3&_limit=2","field":"head.header.access-control-expose-headers","ref":"null","impl":"\"X-Total-Count\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_end=999","field":"header.x-total-count","ref":"null","impl":"\"25\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_end=999","field":"header.access-control-expose-headers","ref":"null","impl":"\"X-Total-Count\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_end=999","field":"head.header.x-total-count","ref":"null","impl":"\"25\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_end=999","field":"head.header.access-control-expose-headers","ref":"null","impl":"\"X-Total-Count\"","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"GET /posts?_end=999","field":"body.len","ref":"len:0","impl":"len:25","reason":"D10: 默认模式识别 0.17 切片写法并发 X-Total-Count（v1 beta.15 把 _start/_end/_limit 当字段过滤，无切片响应头）；见兼容文档 D10"},
    {"plan":"v1","req":"POST /posts","field":"status","ref":"201","impl":"409","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"POST /posts","field":"body.id","ref":"<ID>","impl":"<missing>","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"POST /posts","field":"verify:/posts.len","ref":"len:27","impl":"len:26","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"POST /posts","field":"file.posts.len","ref":"len:27","impl":"len:26","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"POST /mixed","field":"file.posts.len","ref":"len:27","impl":"len:26","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"PUT /posts/2","field":"verify:/posts.len","ref":"len:27","impl":"len:26","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"PUT /posts/2","field":"file.posts.len","ref":"len:27","impl":"len:26","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"PATCH /posts/3","field":"verify:/posts.len","ref":"len:27","impl":"len:26","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"PATCH /posts/3","field":"file.posts.len","ref":"len:27","impl":"len:26","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"PUT /profile","field":"file.posts.len","ref":"len:27","impl":"len:26","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"PATCH /profile","field":"file.posts.len","ref":"len:27","impl":"len:26","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"DELETE /posts/4?_dependent=comments","field":"verify:/posts.len","ref":"len:26","impl":"len:25","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"v1","req":"DELETE /posts/4?_dependent=comments","field":"file.posts.len","ref":"len:26","impl":"len:25","reason":"D4: POST 显式重复 id：v1 beta.15 实测 201 追加（允许重复 id），JsonSrv 409 拒绝并按查重规则不写入；见兼容文档 D4"},
    {"plan":"extrav1","req":"GET /books?labels=x","field":"body.len","ref":"len:0","impl":"len:2","reason":"D10: v1 beta.15 对无冒号参数 labels=x 按字段严格等值（数组字段落空）；JsonSrv 默认模式数组包含命中；见兼容文档 D10"},
    {"plan":"extrav1","req":"GET /books?_where={\"not\":{\"price\":{\"gt\":5}}}","field":"body.len","ref":"len:5","impl":"len:0","reason":"D10: v1 beta.15 实测 _where 的 not 键按未知条件忽略返回全量；JsonSrv 按 mongo 风格 not 取反语义——有意实现更完整的 _where（兼容文档对照表 _where 行）"},
    {"plan":"extrav1","req":"POST /db","field":"verify:/db.status","ref":"404","impl":"200","reason":"D2: v1 beta.15 不提供 GET /db（实测 404），JsonSrv 返回整个数据库（兼容文档对照表）；见兼容文档 D2"},
    {"plan":"default017b","req":"GET /products?sku:eq=007","field":"body.len","ref":"len:6","impl":"len:1","reason":"D13: 冒号操作符（:eq/:in/:ne）是 v1 语法，JsonSrv 默认模式支持（兼容文档对照表 field:op=value 行）；0.17 实测删除该未知参数返回全量——两侧按设计不同"},
    {"plan":"default017b","req":"GET /products?sku:in=007,9","field":"@idseq","ref":"[2]","impl":"[5]","reason":"D13: 冒号操作符（:eq/:in/:ne）是 v1 语法，JsonSrv 默认模式支持（兼容文档对照表 field:op=value 行）；0.17 实测删除该未知参数返回全量——两侧按设计不同"},
    {"plan":"default017b","req":"GET /products?sku:ne=007","field":"@idseq","ref":"[1,2,3,4,5]","impl":"[2,3,4,5,6]","reason":"D13: 冒号操作符（:eq/:in/:ne）是 v1 语法，JsonSrv 默认模式支持（兼容文档对照表 field:op=value 行）；0.17 实测删除该未知参数返回全量——两侧按设计不同"},
    {"plan":"default017b","req":"GET /articles?id:eq=1","field":"body.len","ref":"len:2","impl":"len:1","reason":"D13: 冒号操作符（:eq/:in/:ne）是 v1 语法，JsonSrv 默认模式支持（兼容文档对照表 field:op=value 行）；0.17 实测删除该未知参数返回全量——两侧按设计不同"},
    {"plan":"default017b","req":"GET /remarks?articleId:eq=2","field":"@idseq","ref":"[1,2]","impl":"[2,3]","reason":"D13: 冒号操作符（:eq/:in/:ne）是 v1 语法，JsonSrv 默认模式支持（兼容文档对照表 field:op=value 行）；0.17 实测删除该未知参数返回全量——两侧按设计不同"},
    {"plan":"default017b","req":"GET /products?tags=a","field":"@idseq","ref":"[5]","impl":"[1]","reason":"D14: 数组包含语义，JsonSrv 默认模式命中所有含该值的数组（实测 [1,5]）；0.17 实测仅在单元素数组等于该值时命中（tags [\"a\"] 命中、[\"a\",\"b\"] 不中）——两侧按设计不同"},
    {"plan":"default017b","req":"GET /products?tags=b&tags=c","field":"@idseq","ref":"[2,4]","impl":"[1,2]","reason":"D15: 同名参数重复两侧都是 OR，差异只在数组字段：默认模式按 D14 任一元素相等即命中，0.17 比较整个数组的字符串形式；见兼容文档 D15"},
    {"plan":"default017b","req":"GET /products?dims.w:eq=2","field":"@idseq","ref":"[1,2]","impl":"[2,4]","reason":"D13: 冒号操作符（:eq/:in/:ne）是 v1 语法，JsonSrv 默认模式支持（兼容文档对照表 field:op=value 行）；0.17 实测删除该未知参数返回全量——两侧按设计不同"},
    {"plan":"default017b","req":"GET /products?dims.w:ne=2","field":"@idseq","ref":"[2,3]","impl":"[3,6]","reason":"D13: 冒号操作符（:eq/:in/:ne）是 v1 语法，JsonSrv 默认模式支持（兼容文档对照表 field:op=value 行）；0.17 实测删除该未知参数返回全量——两侧按设计不同"},
    {"plan":"default017b","req":"GET /products?dims.w:in=1,2","field":"@idseq","ref":"[3,4]","impl":"[4,6]","reason":"D13: 冒号操作符（:eq/:in/:ne）是 v1 语法，JsonSrv 默认模式支持（兼容文档对照表 field:op=value 行）；0.17 实测删除该未知参数返回全量——两侧按设计不同"},
    {"plan":"default017b","req":"GET /products?_start=1&_end=4&_sort=name","field":"@idseq","ref":"[5,3,2]","impl":"[2,4,5]","reason":"D18: 字符串大小写，默认模式按 v1 排序规则（小写化比较），0.17 lodash 按原字符码比较；见 兼容文档对照表 _sort 行与 "},
    {"plan":"default017b","req":"GET /products?_limit=2&_embed=x","field":"body[0].x","ref":"[]","impl":"\"<missing>\"","reason":"D16: 未知 _embed 目标，0.17 为每条记录加空数组键 x: []，JsonSrv 默认模式对不存在的集合不加键"},
    {"plan":"default017b","req":"GET /products?_limit=2&_embed=x","field":"body[1].x","ref":"[]","impl":"\"<missing>\"","reason":"D16: 未知 _embed 目标，0.17 为每条记录加空数组键 x: []，JsonSrv 默认模式对不存在的集合不加键"},
    {"plan":"default017b","req":"GET /products?note=null","field":"body.len","ref":"len:0","impl":"len:2","reason":"D17: 默认模式参数 note=null 转换为 null 并匹配 null 字段；0.17 实测不匹配——两侧按设计不同"},
    {"plan":"v1b","req":"GET /products?sku:eq=007","field":"body.len","ref":"len:0","impl":"len:1","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?sku:eq=7","field":"body.len","ref":"len:0","impl":"len:1","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?sku:in=007,9","field":"body.len","ref":"len:0","impl":"len:2","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?sku:ne=007","field":"@idseq","ref":"[1,2,3,4,5]","impl":"[2,3,4,5,6]","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /articles?id:eq=1","field":"body.len","ref":"len:0","impl":"len:1","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /articles?id:in=1,2","field":"body.len","ref":"len:0","impl":"len:2","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /remarks?articleId:eq=2","field":"body.len","ref":"len:0","impl":"len:2","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?dims.w:eq=2","field":"body.len","ref":"len:3","impl":"len:2","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?dims.w:ne=2","field":"body.len","ref":"len:4","impl":"len:3","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?dims.w:ne=2","field":"@idseq","ref":"[5]","impl":"[6]","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?dims.w:in=1,2","field":"body.len","ref":"len:5","impl":"len:4","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?dims.w:in=1,2","field":"@idseq","ref":"[5]","impl":"[6]","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?dims.w:in=1,3","field":"body.len","ref":"len:4","impl":"len:3","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?dims.w:in=1,3","field":"@idseq","ref":"[5]","impl":"[6]","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?dims.h:gt=2&dims.w:lt=3","field":"body.len","ref":"len:3","impl":"len:2","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?tags:in=a","field":"body.len","ref":"len:0","impl":"len:2","reason":"D1: v1 beta.15 实测 :in 对数组字段落空（JsonSrv 数组元素命中）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?_page=1&_per_page=1.5","field":"body.data.len","ref":"len:1","impl":"len:6","reason":"D11: v1 beta.15 实测 _per_page 非法值（1.5/-5）当页大小 1；JsonSrv 按非法值自动修正规则当 10；见兼容文档 D11"},
    {"plan":"v1b","req":"GET /products?_page=1&_per_page=1.5","field":"body.last","ref":"6","impl":"1","reason":"D11: v1 beta.15 实测 _per_page 非法值（1.5/-5）当页大小 1；JsonSrv 按非法值自动修正规则当 10；见兼容文档 D11"},
    {"plan":"v1b","req":"GET /products?_page=1&_per_page=1.5","field":"body.next","ref":"2","impl":"null","reason":"D11: v1 beta.15 实测 _per_page 非法值（1.5/-5）当页大小 1；JsonSrv 按非法值自动修正规则当 10；见兼容文档 D11"},
    {"plan":"v1b","req":"GET /products?_page=1&_per_page=1.5","field":"body.pages","ref":"6","impl":"1","reason":"D11: v1 beta.15 实测 _per_page 非法值（1.5/-5）当页大小 1；JsonSrv 按非法值自动修正规则当 10；见兼容文档 D11"},
    {"plan":"v1b","req":"GET /products?_page=1&_per_page=-5","field":"body.data.len","ref":"len:1","impl":"len:6","reason":"D11: v1 beta.15 实测 _per_page 非法值（1.5/-5）当页大小 1；JsonSrv 按非法值自动修正规则当 10；见兼容文档 D11"},
    {"plan":"v1b","req":"GET /products?_page=1&_per_page=-5","field":"body.last","ref":"6","impl":"1","reason":"D11: v1 beta.15 实测 _per_page 非法值（1.5/-5）当页大小 1；JsonSrv 按非法值自动修正规则当 10；见兼容文档 D11"},
    {"plan":"v1b","req":"GET /products?_page=1&_per_page=-5","field":"body.next","ref":"2","impl":"null","reason":"D11: v1 beta.15 实测 _per_page 非法值（1.5/-5）当页大小 1；JsonSrv 按非法值自动修正规则当 10；见兼容文档 D11"},
    {"plan":"v1b","req":"GET /products?_page=1&_per_page=-5","field":"body.pages","ref":"6","impl":"1","reason":"D11: v1 beta.15 实测 _per_page 非法值（1.5/-5）当页大小 1；JsonSrv 按非法值自动修正规则当 10；见兼容文档 D11"},
    {"plan":"v1b","req":"GET /products?_sort=note","field":"@idseq","ref":"[6,4,3,1]","impl":"[4,3,1,6]","reason":"D10 族：排序 null/缺失值参与方式与 v1 beta.15 实测 sortOn 语义有差异（实测）；兼容文档对照表 _sort 行"},
    {"plan":"v1b","req":"GET /products?_sort=-note","field":"@idseq","ref":"[5,2]","impl":"[2,5]","reason":"D10 族：降序 null 参与方式同上差异"},
    {"plan":"v1b","req":"GET /products?_sort=tags","field":"@idseq","ref":"[3,5,1,2,4]","impl":"[1,2,3,4,5]","reason":"D10 族：数组字段排序比较语义与 v1 beta.15 实测有差（）"},
    {"plan":"v1b","req":"GET /products?_where={\"dims\":{\"w\":{\"in\":[1,3]}}}","field":"body.len","ref":"len:4","impl":"len:3","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?_where={\"dims\":{\"w\":{\"in\":[1,3]}}}","field":"@idseq","ref":"[5]","impl":"[6]","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products?_where={\"or\":[{\"dims\":{\"w\":{\"eq\":3}}},{\"name\":{\"startsWith\":\"A\"}}]}","field":"body.len","ref":"len:3","impl":"len:2","reason":"D1: v1 beta.15 读库把数字 id 字符串化、参数转换与严格相等组合致匹配语义与 JsonSrv 数值等价不同（实测）；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /vendors?_embed=products&_page=1&_per_page=1","field":"body.data[0].products.len","ref":"len:0","impl":"len:3","reason":"D1: v1 beta.15 实测 _embed 外键严格相等（id 已字符串化）嵌不到子集合；JsonSrv 数值等价嵌入；见兼容文档 D1"},
    {"plan":"v1b","req":"GET /products/1?_embed=vendor","field":"body.vendor","ref":"\"<missing>\"","impl":"{\"id\":1,\"name\":\"V1\"}","reason":"D10 族：v1 beta.15 实测单条记录不展开 _embed；JsonSrv 按 兼容文档对照表 _embed 行语义展开父引用（对齐 0.17）"},
    {"plan":"v1b","req":"GET /","field":"body","ref":"@HTML","impl":"@HTML","reason":"D8: 首页 HTML 语义等价豁免（@HTML）；见兼容文档 D8"},
    {"plan":"extrav1","req":"GET /books?nofield=1","field":"body.len","ref":"len:0","impl":"len:5","reason":"D19: 字段在所有记录里都不存在时，默认模式忽略这个参数（与 0.17 一致，防缓存参数 ?_=1699 不会让列表变空）；v1 beta.15 实测返回空数组；见兼容文档 D19"},
    {"plan":"default017b","req":"（规则）","field":"@rule:header.vary","ref":"\"Origin, X-HTTP-Method-Override, Accept-Encoding\"","impl":"\"Origin, Accept-Encoding\"","reason":"D7: 0.17 的 POST 响应 Vary 多一个 X-HTTP-Method-Override，默认模式不发；--legacy 按 0.17 发；见兼容文档 D7"},
    {"plan":"default017b","req":"POST /orders/12e4/lines","field":"header.access-control-expose-headers","ref":"\"Location\"","impl":"null","reason":"D20: 默认模式 POST 按 v1 生成 11 位随机串 id、不发 Location（v1 没有嵌套 POST，集合 POST 也是这样）；0.17 自增数字 id 并发 Location；外键字段本身两侧一致；见兼容文档 D20"},
    {"plan":"default017b","req":"POST /orders/12e4/lines","field":"body.id","ref":"7","impl":"\"<ID>\"","reason":"D20: 默认模式 POST 按 v1 生成 11 位随机串 id、不发 Location（v1 没有嵌套 POST，集合 POST 也是这样）；0.17 自增数字 id 并发 Location；外键字段本身两侧一致；见兼容文档 D20"},
    {"plan":"default017b","req":"POST /orders/12e4/lines","field":"file.lines[6].id","ref":"7","impl":"\"<ID>\"","reason":"D20: 默认模式 POST 按 v1 生成 11 位随机串 id、不发 Location（v1 没有嵌套 POST，集合 POST 也是这样）；0.17 自增数字 id 并发 Location；外键字段本身两侧一致；见兼容文档 D20"},
    {"plan":"default017b","req":"POST /orders/true/lines","field":"header.access-control-expose-headers","ref":"\"Location\"","impl":"null","reason":"D20: 默认模式 POST 按 v1 生成 11 位随机串 id、不发 Location（v1 没有嵌套 POST，集合 POST 也是这样）；0.17 自增数字 id 并发 Location；外键字段本身两侧一致；见兼容文档 D20"},
    {"plan":"default017b","req":"POST /orders/true/lines","field":"body.id","ref":"7","impl":"\"<ID>\"","reason":"D20: 默认模式 POST 按 v1 生成 11 位随机串 id、不发 Location（v1 没有嵌套 POST，集合 POST 也是这样）；0.17 自增数字 id 并发 Location；外键字段本身两侧一致；见兼容文档 D20"},
    {"plan":"default017b","req":"POST /orders/true/lines","field":"file.lines[6].id","ref":"7","impl":"\"<ID>\"","reason":"D20: 默认模式 POST 按 v1 生成 11 位随机串 id、不发 Location（v1 没有嵌套 POST，集合 POST 也是这样）；0.17 自增数字 id 并发 Location；外键字段本身两侧一致；见兼容文档 D20"},
    {"plan":"default017b","req":"POST /orders/nope/lines","field":"header.access-control-expose-headers","ref":"\"Location\"","impl":"null","reason":"D20: 默认模式 POST 按 v1 生成 11 位随机串 id、不发 Location（v1 没有嵌套 POST，集合 POST 也是这样）；0.17 自增数字 id 并发 Location；外键字段本身两侧一致；见兼容文档 D20"},
    {"plan":"default017b","req":"POST /orders/nope/lines","field":"body.id","ref":"7","impl":"\"<ID>\"","reason":"D20: 默认模式 POST 按 v1 生成 11 位随机串 id、不发 Location（v1 没有嵌套 POST，集合 POST 也是这样）；0.17 自增数字 id 并发 Location；外键字段本身两侧一致；见兼容文档 D20"},
    {"plan":"default017b","req":"POST /orders/nope/lines","field":"file.lines[6].id","ref":"7","impl":"\"<ID>\"","reason":"D20: 默认模式 POST 按 v1 生成 11 位随机串 id、不发 Location（v1 没有嵌套 POST，集合 POST 也是这样）；0.17 自增数字 id 并发 Location；外键字段本身两侧一致；见兼容文档 D20"},
]


# 规则级登记只允许每个请求必然相同的 4 类响应头（D6/D7/D9 系列），
# 其余字段一律按请求登记；启动即检查，违反报错退出（防止再从失败输出整类豁免）。
_ALLOWED_RULE_HEADERS = {"content-type", "access-control-allow-origin",
                         "access-control-allow-credentials", "vary"}
_RULE_FIELD_RE = re.compile(r"^(?:head\.)?header\.([a-z-]+)$")
for _idx, _e in enumerate(KNOWN):
    if _e["field"].startswith("@rule:"):
        _m = _RULE_FIELD_RE.match(_e["field"][len("@rule:"):])
        if not _m or _m.group(1) not in _ALLOWED_RULE_HEADERS:
            raise SystemExit(
                "FAIL  规则级登记只允许 4 类响应头（content-type、access-control-allow-origin、"
                "access-control-allow-credentials、vary 及其 head. 形式）；违反: [%s] %s（%s）"
                % (_e["plan"], _e["field"], _e["reason"]))


MISSING = "<missing>"


def norm_url(u):
    if not ("://" in u or u.startswith("/")):
        return u
    try:
        p = urlparse(u)
    except ValueError:
        return u
    netloc = re.sub(r":\d{2,5}$", "", p.netloc)
    q = sorted(parse_qsl(p.query, keep_blank_values=True))
    return urlunparse((p.scheme, netloc, quote(p.path, safe="/:@$"), "", urlencode(q), ""))


def is_fk(key):
    return key == "id" or key.endswith("Id")


def norm_value(key, val):
    """id/外键字段值归一：数字与同值字符串等价，随机串归一。"""
    if key != "id":
        return val
    if isinstance(val, str) and RANDOM_ID.fullmatch(val):
        return "<ID>"
    if isinstance(val, (int, float)) and not isinstance(val, bool):
        return val
    return val


def walk_norm(obj, parent_key=None):
    """递归归一化 JSON：id/外键字段做类型等价与随机值替换；字符串 URL 归一。"""
    # 归一化第 7 项：大整数按 float64 回环等价（见 NORM_RULES 第 7 条、兼容文档 D12）
    if isinstance(obj, int) and not isinstance(obj, bool) and abs(obj) >= (1 << 53):
        return float(obj)
    if isinstance(obj, str):
        s = obj
        if "://" in s or s.startswith("/"):
            s = norm_url(s)
        if parent_key == "id" and RANDOM_ID.fullmatch(s):
            return "<ID>"
        return s
    if isinstance(obj, list):
        return [walk_norm(e) for e in obj]
    if isinstance(obj, dict):
        out = {}
        for k, v in obj.items():
            nv = v
            if is_fk(k):
                if isinstance(nv, str) and nv.isdigit():
                    nv = int(nv)
                elif isinstance(nv, str) and k == "id" and RANDOM_ID.fullmatch(nv):
                    nv = "<ID>"
            out[k] = walk_norm(nv, k)
        return out
    return obj


def canon_json(obj):
    return json.dumps(obj, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def diff_json(ref, impl, path, out):
    """递归生成 (字段, ref值, impl值) 差异列表。"""
    if type(ref) is not type(impl):
        out.append((path, short(canon_json(ref)), short(canon_json(impl))))
        return
    if isinstance(ref, dict):
        for k in sorted(set(ref) | set(impl)):
            a = ref.get(k, MISSING)
            b = impl.get(k, MISSING)
            if a != b:
                diff_json(a, b, path + "." + k if path else k, out)
    elif isinstance(ref, list):
        if len(ref) != len(impl):
            out.append((path + ".len", "len:%d" % len(ref), "len:%d" % len(impl)))
        for i in range(min(len(ref), len(impl))):
            if ref[i] != impl[i]:
                diff_json(ref[i], impl[i], "%s[%d]" % (path, i), out)
    elif ref != impl:
        out.append((path, short(str(ref)), short(str(impl))))


def short(s, n=140):
    return s if len(s) <= n else s[:n] + "…"


class Service:
    def __init__(self, cmd, port, db, log):
        self.cmd = cmd
        self.port = port
        self.proc = None
        env = dict(os.environ)
        self.log = open(log, "w")
        cwd = os.path.dirname(db)
        self.proc = subprocess.Popen(cmd, cwd=cwd, stdout=self.log, stderr=subprocess.STDOUT,
                                     start_new_session=True)
        for _ in range(80):
            try:
                code, _, _ = self.get_raw("http://127.0.0.1:%d/posts" % port, timeout=2)
                if code:
                    return
            except Exception:
                pass
            time.sleep(0.25)
        raise RuntimeError("服务未启动: %s\n%s" % (" ".join(cmd), open(log).read()[-800:]))

    @staticmethod
    def get_raw(url, method="GET", headers=None, data=None, timeout=10):
        req = urllib.request.Request(url, data=data, method=method)
        for k, v in (headers or {}).items():
            req.add_header(k, v)
        try:
            with urllib.request.urlopen(req, timeout=timeout) as res:
                return res.status, {k.lower(): v for k, v in res.headers.items()}, \
                    res.read().decode("utf-8", "replace")
        except urllib.error.HTTPError as e:
            return e.code, {k.lower(): v for k, v in e.headers.items()}, \
                e.read().decode("utf-8", "replace")

    def stop(self):
        if self.proc:
            subprocess.run(["pkill", "-TERM", "-P", str(self.proc.pid)], stderr=subprocess.DEVNULL)
            self.proc.terminate()
            try:
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                subprocess.run(["pkill", "-TERM", "-P", str(self.proc.pid)], stderr=subprocess.DEVNULL)
                self.proc.kill()
            self.log.close()
            self.proc = None


def send(base, method, path, body, headers):
    url = base + quote(path, safe="/?&=,:;@[]{}\"'!*()$%+~#")
    hs = {"Origin": ORIGIN}
    for k, v in headers.items():
        hs[k] = v
    if body is not None and "content-type" not in {k.lower() for k in hs}:
        hs["Content-Type"] = "application/json"
    data = body.encode() if body is not None else None
    return Service.get_raw(url, method=method, headers=hs, data=data)


def parse_body(text):
    try:
        return json.loads(text)
    except (json.JSONDecodeError, ValueError):
        return None


def is_html(text):
    t = text.lstrip()[:60].lower()
    return t.startswith("<!doctype") or t.startswith("<html")


def cmp_pair(label, rstat, rhead, rbody, istat, ihead, ibody, status_name, head_prefix,
             body_path, diffs, skip_body=False):
    if status_name and rstat != istat:
        diffs.append((status_name, str(rstat), str(istat)))
    for h in CMP_HEADERS:
        rv, iv = rhead.get(h), ihead.get(h)
        if h == "link" and (rv or iv):
            rv = sorted((m.group(2), norm_url(m.group(1)))
                        for m in re.finditer(r"<([^>]*)>\s*;\s*rel=\"([^\"]*)\"", rv or "")) or rv
            iv = sorted((m.group(2), norm_url(m.group(1)))
                        for m in re.finditer(r"<([^>]*)>\s*;\s*rel=\"([^\"]*)\"", iv or "")) or iv
        if rv != iv:
            diffs.append((head_prefix + h, canon_json(rv), canon_json(iv)))
    if body_path is None:
        return
    err = isinstance(rstat, int) and rstat >= 400 and rstat == istat
    if err:
        return  # NORM_RULES：错误体文本豁免（状态码一致时）
    if skip_body:
        return
    rj, ij = parse_body(rbody), parse_body(ibody)
    if rj is not None and ij is not None:
        diff_json(walk_norm(rj), walk_norm(ij), body_path, diffs)
    elif is_html(rbody) and is_html(ibody):
        if rbody.strip() != ibody.strip():
            diffs.append((body_path, "@HTML", "@HTML"))
    elif rbody.strip() != ibody.strip():
        diffs.append((body_path, short(rbody.strip()), short(ibody.strip())))


def compare_request(ref_base, impl_base, method, path, body, headers):
    """返回差异列表（字段, ref值, impl值）。含自动 HEAD 与写后验证。"""
    diffs = []
    rs, rh, rb = send(ref_base, method, path, body, headers)
    ks, kh, kb = send(impl_base, method, path, body, headers)
    if method == "HEAD":
        cmp_pair(None, rs, rh, "", ks, kh, "", "head.status", "head.header.", None, diffs)
        return diffs
    cmp_pair(None, rs, rh, rb, ks, kh, kb, "status", "header.", "body", diffs)
    # GET 自动补发 HEAD：状态与响应头（不带体）
    if method == "GET":
        hs, hh, hb = send(ref_base, "HEAD", path, None, headers)
        ts, th, tb = send(impl_base, "HEAD", path, None, headers)
        cmp_pair(None, hs, hh, "", ts, th, "", "head.status", "head.header.", None, diffs)
    # 写请求：验证受影响资源与数据文件
    if method in ("POST", "PUT", "PATCH", "DELETE"):
        res_name = affected_resource(path)
        if res_name:
            vpath = "/" + res_name
            r2, rh2, rb2 = send(ref_base, "GET", vpath, None, headers)
            k2, kh2, kb2 = send(impl_base, "GET", vpath, None, headers)
            cmp_pair(None, r2, {}, rb2, k2, {}, kb2,
                     "verify:" + vpath + ".status", "", "verify:" + vpath, diffs)
    return diffs


def compare_files(ref_db, impl_db, diffs):
    rj, ij = parse_body(open(ref_db, encoding="utf-8").read()), parse_body(open(impl_db, encoding="utf-8").read())
    # NORM：原版 v1 写库后向数据文件写入 $schema 提示字段（JsonSrv 不写），属文件形态差异
    for d in (rj, ij):
        if isinstance(d, dict):
            d.pop("$schema", None)
    if rj is None or ij is None:
        diffs.append(("file.parse", str(rj is not None), str(ij is not None)))
        return
    diff_json(walk_norm(rj), walk_norm(ij), "file", diffs)


def affected_resource(path):
    seg = [s for s in path.split("?")[0].split("/") if s]
    return seg[0] if seg else None


def req_display(method, path):
    return "%s %s" % (method, path)


def load_requests(fn):
    """返回组列表 [(name, [(method, path, body, headers), ...])]。"""
    groups = []
    cur = ("base", [])
    groups.append(cur)
    for line in open(fn, encoding="utf-8"):
        line = line.strip()
        if not line or (line.startswith("#") and not line.startswith("@")):
            continue
        if line.startswith("@group "):
            cur = (line[7:].strip(), [])
            groups.append(cur)
            continue
        parts = line.split(" ", 2)
        method, path = parts[0], parts[1]
        rest = parts[2] if len(parts) > 2 else ""
        headers = {}
        toks = rest.split(" ")
        idx = 0
        for i, t in enumerate(toks):
            if t.startswith("@") and ":" in t:
                k, v = t[1:].split(":", 1)
                headers[k] = v
                idx = i + 1
                continue
            break
        body = " ".join(toks[idx:]).strip() if idx < len(toks) else ""
        cur[1].append((method, path, body or None, headers))
    return [g for g in groups if g[1]]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--plan", choices=["v1", "017", "default017", "extrav1", "extra017",
                   "default017b", "legacy017b", "v1b"])
    ap.add_argument("--requests")
    ap.add_argument("--routes")
    ap.add_argument("--bin")
    ap.add_argument("--refbin")
    ap.add_argument("--db")
    ap.add_argument("--ref-port", type=int, default=3021)
    ap.add_argument("--impl-port", type=int, default=3022)
    ap.add_argument("--impl-flags", default="")
    ap.add_argument("--workdir", default=os.path.join(tempfile.gettempdir(), "jsonsrv-compare"))
    ap.add_argument("--count", action="store_true",
                    help="打印登记总数与各类数量后退出")
    a = ap.parse_args()
    if a.count:
        byplan = {}
        nrule = 0
        for e in KNOWN:
            byplan[e["plan"]] = byplan.get(e["plan"], 0) + 1
            if e["field"].startswith("@rule:"):
                nrule += 1
        print("登记总数=%d  规则级(4类响应头白名单)=%d  请求级=%d" % (len(KNOWN), nrule, len(KNOWN) - nrule))
        for p in sorted(byplan):
            print("  计划 %-12s %d 条" % (p, byplan[p]))
        return
    if not (a.plan and a.requests and a.bin and a.refbin and a.db):
        ap.error("除 --count 外，--plan/--requests/--bin/--refbin/--db 均为必填")

    work = a.workdir
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(work + "/ref")
    os.makedirs(work + "/impl")
    ref_db, impl_db = work + "/ref/db.json", work + "/impl/db.json"

    impl_flags = shlex.split(a.impl_flags)

    def start_services():
        for d in (ref_db, impl_db):
            shutil.copyfile(a.db, d)
        # 明确绑定 IPv4：localhost 在有些机器上解析成 ::1（如 GitHub 的机器），而这里按 127.0.0.1 访问。
        ref_cmd = [a.refbin, "db.json", "--port", str(a.ref_port), "--host", "127.0.0.1"]
        impl_cmd = [a.bin, "db.json", "--port", str(a.impl_port), "--host", "127.0.0.1"] + impl_flags
        if a.routes:
            ref_cmd += ["--routes", os.path.abspath(a.routes)]
            impl_cmd += ["--routes", os.path.abspath(a.routes)]
        # 各计划的语义（compare stage 传参决定，start_services 只按参数拼装）：
        #   v1/extrav1：ref = v1 beta 默认模式；impl = JsonSrv 默认模式。
        #   017：ref = 0.17 默认（原生 legacy）；impl = JsonSrv --legacy（+ --routes 同文件）。
        #   default017：ref = 0.17 默认；impl = JsonSrv 默认模式（R1：识别 0.17 写法）。
        #   extra017：ref = 0.17 默认；impl = JsonSrv --legacy，数据 compare-db2.json。
        if a.plan == "v1":
            pass
        r = Service(ref_cmd, a.ref_port, ref_db, work + "/ref.log")
        i = Service(impl_cmd, a.impl_port, impl_db, work + "/impl.log")
        return r, i

    ref_base = "http://127.0.0.1:%d" % a.ref_port
    impl_base = "http://127.0.0.1:%d" % a.impl_port

    ok = known = fail = 0
    matched = set()
    rules_seen = set()  # 规则级/id 序列登记的实际出现记录
    failures = []
    groups = load_requests(a.requests)
    gi = 0
    for name, requests in groups:
        gi += 1
        r, i = start_services()
        try:
            for (method, path, body, headers) in requests:
                key = req_display(method, path)
                diffs = compare_request(ref_base, impl_base, method, path, body, headers)
                if method in ("POST", "PUT", "PATCH", "DELETE"):
                    compare_files(ref_db, impl_db, diffs)
                fresh = []
                # 两类登记——
                #   规则级（field "@rule:<字段名>"）：每请求都出现的系统性差异，值写死、跨请求复用；
                #     只命中规则级登记的请求计为 ok（这是 v1 计划 ok 不再为 0 的来源）。
                #   请求级（field 精确名或 "@idseq"）：单请求差异，逐条手写理由与兼容文档编号。
                # 结果集顺序差异按 id 序列一条登记（field "@idseq"），整文档差异由这一条覆盖。
                idseq_covered = False
                req_known = 0
                rule_hits = 0
                for (field, rv, iv) in diffs:
                    hit = None
                    for idx, e in enumerate(KNOWN):
                        if (e["plan"] == a.plan and e["req"] == key and e["field"] == field
                                and (key, idx) not in matched and reg_val(e["ref"], rv) and reg_val(e["impl"], iv)):
                            hit = idx
                            break
                    if hit is not None:
                        matched.add((key, hit))
                        req_known += 1
                        continue
                    for idx, e in enumerate(KNOWN):
                        if (e["plan"] == a.plan and e["field"] == "@rule:" + field
                                and reg_val(e["ref"], rv) and reg_val(e["impl"], iv)):
                            hit = idx
                            rules_seen.add("rule#" + str(idx))
                            rule_hits += 1
                            break
                    if hit is None:
                        fresh.append((field, rv, iv))
                # 结果集顺序差异（@idseq 一条登记）在规则归一之后评估：
                # 剩余 fresh 若全部是 body 族差异且含 id 条目差异，则拼出两侧 id 序列匹配 @idseq 登记。
                if fresh and any(f.startswith("body") for (f, _r, _i) in fresh):
                    rseq = []
                    iseq = []
                    for (f, rv, iv) in fresh:
                        m = re.fullmatch(r"body(?:\.\w+)*\[(\d+)\]\.id", f)
                        if m:
                            i0 = int(m.group(1))
                            rseq.append((i0, rv))
                            iseq.append((i0, iv))
                    rseq.sort()
                    iseq.sort()
                    if rseq and iseq:
                        rj = "[" + ",".join(x[1] for x in rseq) + "]"
                        ij = "[" + ",".join(x[1] for x in iseq) + "]"
                        for idx, e in enumerate(KNOWN):
                            if (e["plan"] == a.plan and e["req"] == key and e["field"] == "@idseq"
                                    and (key, idx) not in matched and e["ref"] == rj and e["impl"] == ij):
                                matched.add((key, idx))
                                print("KNOWN [%s] %s (id 序列登记:%s 到 %s)" % (name, key, rj, ij))
                                idseq_covered = True
                                break
                        if idseq_covered:
                            fresh = [d for d in fresh if not d[0].startswith("body")]
                        else:
                            fresh.append(("@idseq", rj, ij))
                if fresh:
                    fail += 1
                    print("FAIL  [%s] %s" % (name, key))
                    for (field, rv, iv) in fresh:
                        print("      %s: ref=%s impl=%s" % (field, short(rv), short(iv)))
                elif req_known or idseq_covered:
                    known += 1
                    print("KNOWN [%s] %s (%d 项请求级登记差异)" % (name, key, req_known + int(idseq_covered)))
                else:
                    ok += 1
                    tail = ("（规则级归一 %d 项）" % rule_hits) if rule_hits else ""
                    print("PASS  [%s] %s%s" % (name, key, tail))
        finally:
            r.stop()
            i.stop()
            r.stop()
            i.stop()

    unused = [e for idx, e in enumerate(KNOWN)
              if e["plan"] == a.plan and not any(m[1] == idx for m in matched)
              and "rule#" + str(idx) not in rules_seen]
    for e in unused:
        fail += 1
        print("FAIL  登记未出现: [%s] %s %s（理由: %s）" % (e["plan"], e["req"], e["field"], e["reason"]))
    dup = len(KNOWN) - len(set((e["plan"], e["req"], e["field"]) for e in KNOWN if e["plan"] == a.plan))
    print()
    print("归一化规则（登记）:")
    for name, reason in NORM_RULES:
        print("  - %s: %s" % (name, reason))
    # 编号双向校验——每条登记的理由必须带 兼容文档「已知差异」编号（D+数字）；
    # 兼容文档出现的每个编号至少被一条登记或 NORM 规则文本引用；登记的编号必须在兼容文档存在。
    import os.path as _osp
    _doc = ""
    try:
        with open(_osp.join(_osp.dirname(_osp.abspath(__file__)), "..", "docs", "COMPATIBILITY.md"), encoding="utf-8") as _f:
            _doc = _f.read()
    except OSError:
        print("FAIL  编号校验：读不到 docs/COMPATIBILITY.md")
        fail += 1
    d_doc = set(re.findall(r"\bD\d+\b", _doc))
    d_known = set()
    for e in KNOWN:
        ds = re.findall(r"\bD\d+\b", e["reason"])
        if not ds:
            print("FAIL  登记缺兼容文档编号: [%s] %s %s（理由: %s）" % (e["plan"], e["req"], e["field"], e["reason"]))
            fail += 1
        d_known.update(ds)
    d_norm = set(re.findall(r"\bD\d+\b", chr(10).join(n + " " + r for n, r in NORM_RULES)))
    for d in sorted(d_known | d_norm):
        if d not in d_doc:
            print("FAIL  登记的编号不在兼容文档: %s" % d)
            fail += 1
    for d in sorted(d_doc):
        if d not in d_known and d not in d_norm:
            print("FAIL  兼容文档编号未被登记或规则引用: %s" % d)
            fail += 1
    print("SUMMARY plan=%s ok=%d known=%d fail=%d" % (a.plan, ok, known, fail))
    sys.exit(1 if fail else 0)


def reg_val(want, actual):
    # 登记值一律写死精确匹配（@ANY 已禁用）；@HTML 只用于首页 HTML 语义等价登记。
    return want == "@HTML" or want == actual


if __name__ == "__main__":
    main()
