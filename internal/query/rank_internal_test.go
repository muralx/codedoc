package query

import (
	"math"
	"reflect"
	"testing"
)

func TestByCosineMergesAndDropsFarHits(t *testing.T) {
	sym, mod, dep := hit{id: 1}, hit{module: true, id: 1}, hit{dep: true, id: 1}
	far := hit{id: 9}
	got := byCosine([]scoredHit{{far, 0.7 - vecMargin - 0.01}, {mod, 0.6}, {dep, 0.6}, {sym, 0.6}, {hit{id: 2}, 0.7}})
	// The three hits at 0.6 tie: the symbols, the workspace's first,
	// then the module.
	if want := []hit{{id: 2}, sym, dep, mod}; !reflect.DeepEqual(got.hits, want) {
		t.Fatalf("%+v", got)
	}
	// 0.1 below the best is twenty steps of rank.
	if got.rankOf(0) != 0 || math.Abs(got.rankOf(1)-0.1/cosineStep) > 1e-6 || got.rankOf(1) != got.rankOf(3) {
		t.Fatalf("%+v", got.ranks)
	}
	if got := byCosine(nil); got.hits != nil {
		t.Fatalf("%+v", got)
	}
	// A hit that leads the vector list by a clear gap beats one a place
	// behind it there and two places ahead in the text list.
	lead, other := hit{id: 1}, hit{id: 2}
	vec := byCosine([]scoredHit{{lead, 0.7}, {other, 0.65}})
	if got, _ := fuse(func(hit) bool { return false }, ordinal([]hit{other, {id: 3}, lead}), vec); got[0] != lead {
		t.Fatalf("%+v", got)
	}
}

func TestByCoverageRanksHitsHoldingMoreTermsFirst(t *testing.T) {
	texts := map[hit]string{
		{id: 1}:               "sends a notification",
		{id: 2}:               "notification sent to the client by the server",
		{id: 3}:               "client",
		{module: true, id: 1}: "the client package notifies",
		{id: 4}:               "nothing of the query",
	}
	terms := []string{"send", "notification", "client", "server"}
	symbols := []hit{{id: 3}, {id: 1}, {id: 2}, {id: 4}}
	sections := []hit{{id: 2}}
	modules := []hit{{module: true, id: 1}}
	textOf := func(h hit) string { return texts[h] }
	none := func(hit) bool { return false }
	got := byCoverage([][]hit{symbols, sections, modules}, terms, textOf, none)
	// Hit 2 holds three terms (sent is not send), hit 1 two; the module
	// holds one less than half of three, as do hits 3 and 4: dropped.
	if want := []hit{{id: 2}, {id: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	// A hit the vectors found too stays, behind those holding more.
	got = byCoverage([][]hit{symbols, sections, modules}, terms, textOf, func(h hit) bool { return h == hit{id: 3} })
	if want := []hit{{id: 2}, {id: 1}, {id: 3}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	// Equal coverage: the better rank in its own list first, then
	// symbols before modules.
	same := func(hit) string { return "client" }
	got = byCoverage([][]hit{{{id: 5}, {id: 6}}, {{module: true, id: 7}}}, []string{"client"}, same, none)
	if want := []hit{{id: 5}, {module: true, id: 7}, {id: 6}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
}

func TestSpreadKeepsSectionsOfOneDocumentApart(t *testing.T) {
	docs := map[hit]int64{{id: 1}: 7, {id: 2}: 7, {id: 3}: 7, {id: 5}: 8}
	docOf := func(h hit) int64 { return docs[h] }
	code, other := hit{id: 4}, hit{id: 5}
	got := spread([]hit{{id: 1}, {id: 2}, {id: 3}, code, other}, docOf)
	if want := []hit{{id: 1}, code, {id: 2}, other, {id: 3}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	// Without a hit from elsewhere the sections keep their order.
	if got := spread([]hit{{id: 1}, {id: 2}, {id: 3}}, docOf); !reflect.DeepEqual(got, []hit{{id: 1}, {id: 2}, {id: 3}}) {
		t.Fatalf("%+v", got)
	}
	if got := spread([]hit{code, {module: true, id: 4}}, docOf); len(got) != 2 || got[0] != code {
		t.Fatalf("%+v", got)
	}
}

func TestSearchTermsDropStopWordsAndEndings(t *testing.T) {
	for text, want := range map[string][]string{
		"where is the bearer token checked":    {"bearer", "token", "check"},
		"how are provisional agents reclaimed": {"provisional", "agent", "reclaim"},
		"Polling the class status":             {"poll", "class", "statu"},
		"used keys":                            {"used", "keys"},
		"agent agents":                         {"agent"},
		"how is it":                            {"how", "is", "it"},
		"":                                     nil,
	} {
		if got := searchTerms(text); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}
