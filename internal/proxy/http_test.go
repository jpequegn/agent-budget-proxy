package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPBoundary(t *testing.T) {
	e, cap := setup(t)
	mock := &MockTool{}
	cred := Credentials{Admin: strings.Repeat("a", 32), Reviewer: strings.Repeat("r", 32), ReviewerID: "independent"}
	handler, err := HTTP(e, mock, cred)
	if err != nil {
		t.Fatal(err)
	}
	call := func(path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if call("/runs", cap, `{}`).Code != 401 {
		t.Fatal("agent minted root run")
	}
	if call("/actions", cap, `{"key":"a","agent":"forged","verb":"inspect","resource":"inspect","units":1,"data_class":"public"}`).Code != 400 {
		t.Fatal("identity accepted")
	}
	if call("/actions", cap, `{"key":"a","cost_micros":0,"verb":"inspect","resource":"inspect","units":1,"data_class":"public"}`).Code != 400 {
		t.Fatal("price accepted")
	}
	body := `{"key":"delete","verb":"delete","resource":"delete","units":1,"data_class":"internal"}`
	response := call("/actions", cap, body)
	if response.Code != 202 || mock.Calls() != 0 {
		t.Fatal(response.Body.String())
	}
	var action Action
	if err = json.NewDecoder(response.Body).Decode(&action); err != nil {
		t.Fatal(err)
	}
	approval, _ := json.Marshal(map[string]string{"id": action.ID, "digest": action.ApprovalDigest})
	if call("/approvals", cap, string(approval)).Code != 401 {
		t.Fatal("agent approved")
	}
	if call("/approvals", cred.Admin, string(approval)).Code != 401 {
		t.Fatal("operator used second key")
	}
	if call("/approvals", cred.Reviewer, string(approval)).Code != 200 {
		t.Fatal("reviewer failed")
	}
	if response = call("/actions", cap, body); response.Code != 200 || mock.Calls() != 1 {
		t.Fatal(response.Body.String())
	}
	if call("/actions", cap, body).Code != 200 || mock.Calls() != 1 {
		t.Fatal("HTTP retry repeated write")
	}
	if call("/actions", cap, string(bytes.Repeat([]byte(" "), 17000))+`{}`).Code != 400 {
		t.Fatal("oversized request")
	}
}
