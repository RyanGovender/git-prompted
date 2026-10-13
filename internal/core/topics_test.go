package core

import (
	"strings"
	"testing"
)

func TestValidateTopicRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		`{"id":"Bad ID","patterns":["x"]}`:               `kebab-case, e.g. "bad-id"`,
		`{"id":"a","patterns":["use(State"]}`:            `pattern 1 "use(State" does not compile`,
		`{"id":"a"}`:                                     "at least one pattern or path",
		`{"id":"a","mode":"sometimes","patterns":["x"]}`: "mode",
		`{"patterns":["x"]}`:                             "id is required",
		`{"id":"a","pattern":["x"]}`:                     "invalid topic JSON",
		`{"id":"a","patterns":"x"}`:                      "invalid topic JSON",
		`[]`:                                             "no topics",
	}
	for in, want := range cases {
		ts, err := ParseTopicsJSON([]byte(in))
		if err == nil {
			err = ValidateTopic(ts[0])
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want error containing %q", in, err, want)
		}
	}
}

func TestParseTopicsJSONAcceptsObjectOrArray(t *testing.T) {
	one, err := ParseTopicsJSON([]byte(` {"id":"a","patterns":["x"],"concepts":["Error Wrapping","error-wrapping"]} `))
	if err != nil || len(one) != 1 {
		t.Fatalf("object: %v %v", one, err)
	}
	if err := ValidateTopic(one[0]); err != nil {
		t.Fatal(err)
	}
	if strings.Join(one[0].Concepts, ",") != "error-wrapping" {
		t.Fatalf("concepts not normalised: %v", one[0].Concepts)
	}
	many, err := ParseTopicsJSON([]byte(`[{"id":"a","paths":["docs/**"]},{"id":"b","mode":"quiz-after","patterns":["y"]}]`))
	if err != nil || len(many) != 2 {
		t.Fatalf("array: %v %v", many, err)
	}
	for _, tp := range many {
		if err := ValidateTopic(tp); err != nil {
			t.Fatal(err)
		}
	}
}
