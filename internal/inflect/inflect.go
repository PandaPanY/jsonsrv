// Package inflect 提供英文单复数转换，移植自 json-server 0.17 依赖的 pluralize 库默认规则，
// 供外键推导（_embed/_expand/_dependent/嵌套路由）使用。规则表、不规则词表与不可数词表
// 摘自 pluralize 源码（MIT，见 THIRD_PARTY_NOTICES.md），期望词表由原库实测生成，测试逐词比对。
package inflect

import (
	"regexp"
	"strings"
)

// irregularSingles：不规则词单数→复数（原版 addIrregularRule 列表）。
var irregularSingles = map[string]string{
	// 代词。
	"i":        "we",
	"me":       "us",
	"he":       "they",
	"she":      "they",
	"them":     "them",
	"myself":   "ourselves",
	"yourself": "yourselves",
	"itself":   "themselves",
	"herself":  "themselves",
	"himself":  "themselves",
	"themself": "themselves",
	"is":       "are",
	"was":      "were",
	"has":      "have",
	"this":     "these",
	"that":     "those",
	// 以“辅音+o”结尾的词。
	"echo":    "echoes",
	"dingo":   "dingoes",
	"volcano": "volcanoes",
	"tornado": "tornadoes",
	"torpedo": "torpedoes",
	// 以 us 结尾（拉丁源）。
	"genus":  "genera",
	"viscus": "viscera",
	// 以 ma 结尾（希腊源）。
	"stigma":   "stigmata",
	"stoma":    "stomata",
	"dogma":    "dogmata",
	"lemma":    "lemmata",
	"schema":   "schemata",
	"anathema": "anathemata",
	// 其他不规则词。
	"ox":       "oxen",
	"axe":      "axes",
	"die":      "dice",
	"yes":      "yeses",
	"foot":     "feet",
	"eave":     "eaves",
	"goose":    "geese",
	"tooth":    "teeth",
	"quiz":     "quizzes",
	"human":    "humans",
	"proof":    "proofs",
	"carve":    "carves",
	"valve":    "valves",
	"looey":    "looies",
	"thief":    "thieves",
	"groove":   "grooves",
	"pickaxe":  "pickaxes",
	"passerby": "passersby",
	"person":   "people",
	"man":      "men",
	"woman":    "women",
	"child":    "children",
	"mouse":    "mice",
	"loaf":     "loaves",
	"knife":    "knives",
}

// irregularPlurals 是复数→单数反向表。与原版一致：同一复数有多个来源时，
// 按列表顺序后写入者覆盖前者（he/she→they 最终为 she）。
var irregularPlurals = map[string]string{
	"we":         "i",
	"us":         "me",
	"they":       "she", // 原版后写入的 she 覆盖 he
	"them":       "them",
	"ourselves":  "myself",
	"yourselves": "yourself",
	"themselves": "themself",
	"are":        "is",
	"were":       "was",
	"have":       "has",
	"these":      "this",
	"those":      "that",
	"echoes":     "echo",
	"dingoes":    "dingo",
	"volcanoes":  "volcano",
	"tornadoes":  "tornado",
	"torpedoes":  "torpedo",
	"genera":     "genus",
	"viscera":    "viscus",
	"stigmata":   "stigma",
	"stomata":    "stoma",
	"dogmata":    "dogma",
	"lemmata":    "lemma",
	"schemata":   "schema",
	"anathemata": "anathema",
	"oxen":       "ox",
	"axes":       "axe",
	"dice":       "die",
	"yeses":      "yes",
	"feet":       "foot",
	"eaves":      "eave",
	"geese":      "goose",
	"teeth":      "tooth",
	"quizzes":    "quiz",
	"humans":     "human",
	"proofs":     "proof",
	"carves":     "carve",
	"valves":     "valve",
	"looies":     "looey",
	"thieves":    "thief",
	"grooves":    "groove",
	"pickaxes":   "pickaxe",
	"passersby":  "passerby",
	"people":     "person",
	"men":        "man",
	"women":      "woman",
	"children":   "child",
	"mice":       "mouse",
	"loaves":     "loaf",
	"knives":     "knife",
}

// uncountables：不可数词（小写 token 直接命中）。
var uncountables = map[string]struct{}{
	"adulthood": {}, "advice": {}, "agenda": {}, "aid": {}, "aircraft": {}, "alcohol": {},
	"ammo": {}, "analytics": {}, "anime": {}, "athletics": {}, "audio": {}, "bison": {},
	"blood": {}, "bream": {}, "buffalo": {}, "butter": {}, "carp": {}, "cash": {},
	"chassis": {}, "chess": {}, "clothing": {}, "cod": {}, "commerce": {}, "cooperation": {},
	"corps": {}, "debris": {}, "diabetes": {}, "digestion": {}, "elk": {}, "energy": {},
	"equipment": {}, "excretion": {}, "expertise": {}, "firmware": {}, "flounder": {},
	"fun": {}, "gallows": {}, "garbage": {}, "graffiti": {}, "hardware": {},
	"headquarters": {}, "health": {}, "herpes": {}, "highjinks": {}, "homework": {},
	"housework": {}, "information": {}, "jeans": {}, "justice": {}, "kudos": {},
	"labour": {}, "literature": {}, "machinery": {}, "mackerel": {}, "mail": {}, "media": {},
	"mews": {}, "moose": {}, "music": {}, "mud": {}, "manga": {}, "news": {}, "only": {},
	"personnel": {}, "pike": {}, "plankton": {}, "pliers": {}, "police": {}, "pollution": {},
	"premises": {}, "rain": {}, "research": {}, "rice": {}, "salmon": {}, "scissors": {},
	"series": {}, "sewage": {}, "shambles": {}, "shrimp": {}, "software": {}, "species": {},
	"staff": {}, "swine": {}, "tennis": {}, "traffic": {}, "transportation": {}, "trout": {},
	"tuna": {}, "wealth": {}, "welfare": {}, "whiting": {}, "wildebeest": {}, "wildlife": {},
	"you": {},
}

// uncountableRe：原版 addUncountableRule 对正则词尾的 $0 规则（原样保留），
// 追加到单复数规则表尾部，因此反向遍历时最先命中。
var uncountableRe = []*regexp.Regexp{
	regexp.MustCompile(`(?i)pok[efé]mon$`),
	regexp.MustCompile(`(?i)[^aeiou]ese$`),
	regexp.MustCompile(`(?i)deer$`),
	regexp.MustCompile(`(?i)fish$`),
	regexp.MustCompile(`(?i)measles$`),
	regexp.MustCompile(`(?i)o[iu]s$`),
	regexp.MustCompile(`(?i)pox$`),
	regexp.MustCompile(`(?i)sheep$`),
}

// rule 是一条正则替换规则（(?i) 前缀，repl 用 $1 形式）。
type rule struct {
	re   *regexp.Regexp
	repl string
}

func rx(s string) *regexp.Regexp { return regexp.MustCompile("(?i)" + s) }

// pluralRuleList：原版 addPluralRule 列表顺序；使用时从后向前（后者优先）。
var pluralRuleList = []rule{
	{rx(`s?$`), "s"},
	{rx(`[^\x00-\x7F]$`), "$0"},
	{rx(`([^aeiou]ese)$`), "$1"},
	{rx(`(ax|test)is$`), "$1es"},
	{rx(`(alias|[^aou]us|t[lm]as|gas|ris)$`), "$1es"},
	{rx(`(e[mn]u)s?$`), "$1s"},
	{rx(`([^l]ias|[aeiou]las|[ejzr]as|[iu]am)$`), "$1"},
	{rx(`(alumn|syllab|vir|radi|nucle|fung|cact|stimul|termin|bacill|foc|uter|loc|strat)(?:us|i)$`), "$1i"},
	{rx(`(alumn|alg|vertebr)(?:a|ae)$`), "$1ae"},
	{rx(`(seraph|cherub)(?:im)?$`), "$1im"},
	{rx(`(her|at|gr)o$`), "$1oes"},
	{rx(`(agend|addend|millenni|dat|extrem|bacteri|desiderat|strat|candelabr|errat|ov|symposi|curricul|automat|quor)(?:a|um)$`), "$1a"},
	{rx(`(apheli|hyperbat|periheli|asyndet|noumen|phenomen|criteri|organ|prolegomen|hedr|automat)(?:a|on)$`), "$1a"},
	{rx(`sis$`), "ses"},
	{rx(`(?:(kni|wi|li)fe|(ar|l|ea|eo|oa|hoo)f)$`), "$1$2ves"},
	{rx(`([^aeiouy]|qu)y$`), "$1ies"},
	{rx(`([^ch][ieo][ln])ey$`), "$1ies"},
	{rx(`(x|ch|ss|sh|zz)$`), "$1es"},
	{rx(`(matr|cod|mur|sil|vert|ind|append)(?:ix|ex)$`), "$1ices"},
	{rx(`\b((?:tit)?m|l)(?:ice|ouse)$`), "$1ice"},
	{rx(`(pe)(?:rson|ople)$`), "$1ople"},
	{rx(`(child)(?:ren)?$`), "$1ren"},
	{rx(`eaux$`), "$0"},
	{rx(`m[ae]n$`), "men"},
	{rx(`^thou$`), "you"},
}

// singularRuleList：原版 addSingularRule 列表顺序；同样从后向前命中。
var singularRuleList = []rule{
	{rx(`s$`), ""},
	{rx(`(ss)$`), "$1"},
	{rx(`(wi|kni|(?:after|half|high|low|mid|non|night|[^\w]|^)li)ves$`), "$1fe"},
	{rx(`(ar|(?:wo|[ae])l|[eo][ao])ves$`), "$1f"},
	{rx(`ies$`), "y"},
	{rx(`\b([pl]|zomb|(?:neck|cross)?t|coll|faer|food|gen|goon|group|lass|talk|goal|cut)ies$`), "$1ie"},
	{rx(`\b(mon|smil)ies$`), "$1ey"},
	{rx(`\b((?:tit)?m|l)ice$`), "$1ouse"},
	{rx(`(seraph|cherub)im$`), "$1"},
	{rx(`(x|ch|ss|sh|zz|tto|go|cho|alias|[^aou]us|t[lm]as|gas|(?:her|at|gr)o|[aeiou]ris)(?:es)?$`), "$1"},
	{rx(`(analy|diagno|parenthe|progno|synop|the|empha|cri|ne)(?:sis|ses)$`), "$1sis"},
	{rx(`(movie|twelve|abuse|e[mn]u)s$`), "$1"},
	{rx(`(test)(?:is|es)$`), "$1is"},
	{rx(`(alumn|syllab|vir|radi|nucle|fung|cact|stimul|termin|bacill|foc|uter|loc|strat)(?:us|i)$`), "$1us"},
	{rx(`(agend|addend|millenni|dat|extrem|bacteri|desiderat|strat|candelabr|errat|ov|symposi|curricul|quor)a$`), "$1um"},
	{rx(`(apheli|hyperbat|periheli|asyndet|noumen|phenomen|criteri|organ|prolegomen|hedr|automat)a$`), "$1on"},
	{rx(`(alumn|alg|vertebr)ae$`), "$1a"},
	{rx(`(cod|mur|sil|vert|ind)ices$`), "$1ex"},
	{rx(`(matr|append)ices$`), "$1ix"},
	{rx(`(pe)(rson|ople)$`), "$1rson"},
	{rx(`(child)ren$`), "$1"},
	{rx(`(eau)x?$`), "$1"},
	{rx(`men$`), "man"},
}

// singularizeRules/pluralizeRules 把不可数正则规则追加到表尾（原版 addUncountableRule
// 在 addPluralRule/addSingularRule 之后调用，因此反向优先命中）。
var (
	singularizeRules = appendRules(singularRuleList, uncountableRe)
	pluralizeRules   = appendRules(pluralRuleList, uncountableRe)
)

func appendRules(base []rule, res []*regexp.Regexp) []rule {
	out := make([]rule, len(base))
	copy(out, base)
	for _, re := range res {
		out = append(out, rule{re, "$0"})
	}
	return out
}

// Singular 返回词的单数形式；无法识别时原样返回（与原版一致）。
func Singular(word string) string {
	lower := strings.ToLower(word)
	// 已是单数（不规则单数表命中）时原样返回。
	if _, ok := irregularSingles[lower]; ok {
		return word
	}
	if s, ok := irregularPlurals[lower]; ok {
		return restoreCase(word, s)
	}
	if _, ok := uncountables[lower]; ok {
		return word
	}
	return transform(word, lower, singularizeRules)
}

// Plural 返回词的复数形式；无法识别时原样返回。
func Plural(word string) string {
	lower := strings.ToLower(word)
	if p, ok := irregularSingles[lower]; ok {
		return restoreCase(word, p)
	}
	if _, ok := irregularPlurals[lower]; ok {
		return word
	}
	if _, ok := uncountables[lower]; ok {
		return word
	}
	return transform(word, lower, pluralizeRules)
}

// replGroupRe 用于把原库 interpolate 的 $1 形式替换串转成 Go 的 ${1} 形式。
// Go 中 $1es 会被解析成组名 "1es"，因此组号后跟字母时必须加花括号。
var replGroupRe = regexp.MustCompile(`\$\d+`)

// transform 按原版 sanitizeWord+replace 语义：从规则表尾向前找第一条命中的规则，
// 替换首个匹配片段并按原词大小写风格还原。
func transform(word, lower string, rules []rule) string {
	for i := len(rules) - 1; i >= 0; i-- {
		r := rules[i]
		if !r.re.MatchString(lower) {
			continue
		}
		repl := replGroupRe.ReplaceAllStringFunc(r.repl, func(m string) string {
			return "${" + m[1:] + "}"
		})
		result := r.re.ReplaceAllString(lower, repl)
		return restoreCase(word, result)
	}
	return word
}

// restoreCase 按原词大小写风格调整替换结果（原版 restoreCase）。
func restoreCase(word, token string) string {
	if word == token {
		return token
	}
	if word == strings.ToLower(word) {
		return strings.ToLower(token)
	}
	if word == strings.ToUpper(word) {
		return strings.ToUpper(token)
	}
	if len(word) > 0 && word[0:1] == strings.ToUpper(word[0:1]) {
		r := []rune(token)
		if len(r) > 0 {
			r[0] = []rune(strings.ToUpper(string(r[0])))[0]
			return string(r)
		}
	}
	return strings.ToLower(token)
}
