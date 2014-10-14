// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package storetesting

import (
	"encoding/json"
	"fmt"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
)

// JSONEquals defines a checker that checks whether a byte slice, when
// unmarshaled as JSON, is equal to the given value.
// Rather than unmarshaling into something of the expected
// body type, we reform the expected body in JSON and
// back to interface{}, so we can check the whole content.
// Otherwise we lose information when unmarshaling.
var JSONEquals = &jsonEqualChecker{
	&gc.CheckerInfo{Name: "JSONEquals", Params: []string{"obtained", "expected"}},
}

type jsonEqualChecker struct {
	*gc.CheckerInfo
}

func (checker *jsonEqualChecker) Check(params []interface{}, names []string) (result bool, error string) {
	gotContent, ok := params[0].([]byte)
	if !ok {
		return false, fmt.Sprintf("expected []byte, got %T", params[0])
	}
	expectContent := params[1]
	expectContentBytes, err := json.Marshal(expectContent)
	if err != nil {
		return false, fmt.Sprintf("cannot marshal expected contents: %v", err)
	}
	var expectContentVal interface{}
	if err := json.Unmarshal(expectContentBytes, &expectContentVal); err != nil {
		return false, fmt.Sprintf("cannot unmarshal expected contents: %v", err)
	}

	var gotContentVal interface{}
	if err := json.Unmarshal(gotContent, &gotContentVal); err != nil {
		return false, fmt.Sprintf("cannot unmarshal obtained contents: %v; %q", err, gotContent)
	}

	if ok, err := jc.DeepEqual(gotContentVal, expectContentVal); !ok {
		return false, err.Error()
	}
	return true, ""
}
