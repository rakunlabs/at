package service

import (
	"encoding/json"
	"testing"
)

func TestListResultJSON(t *testing.T) {
	r := ListResult[string]{}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"data":[],"meta":{"total":0,"offset":0,"limit":0}}` {
		t.Fatalf("empty list: %s", b)
	}
	if r.Data != nil {
		t.Fatal("marshalling mutated the result")
	}
	r.Data = []string{"item"}
	r.Meta = ListMeta{Total: 1, Offset: 2, Limit: 10}
	b, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"data":["item"],"meta":{"total":1,"offset":2,"limit":10}}` {
		t.Fatalf("populated list: %s", b)
	}
}
