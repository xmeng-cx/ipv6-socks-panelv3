package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func loginCookie(t *testing.T, handler http.Handler, username, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK || len(recorder.Result().Cookies()) == 0 {
		t.Fatalf("login failed: %d %s", recorder.Code, recorder.Body.String())
	}
	return recorder.Result().Cookies()[0]
}

func TestSessionLoginAndUserIsolation(t *testing.T) {
	cfg := testConfig(t)
	manager := NewManager(cfg, newFakeNetwork(), &fakeXray{})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(manager)

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/proxies", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorized.Code)
	}

	adminCookie := loginCookie(t, handler, "xmeng", "5201314")
	addUserBody := bytes.NewBufferString(`{"username":"alice","password":"alice123"}`)
	addUserReq := httptest.NewRequest(http.MethodPost, "/api/v1/users", addUserBody)
	addUserReq.AddCookie(adminCookie)
	addUserResult := httptest.NewRecorder()
	handler.ServeHTTP(addUserResult, addUserReq)
	if addUserResult.Code != http.StatusCreated {
		t.Fatalf("add user failed: %d %s", addUserResult.Code, addUserResult.Body.String())
	}

	aliceCookie := loginCookie(t, handler, "alice", "alice123")
	addProxyReq := httptest.NewRequest(http.MethodPost, "/api/v1/proxies", bytes.NewBufferString(`{"protocol":"socks5"}`))
	addProxyReq.AddCookie(aliceCookie)
	addProxyResult := httptest.NewRecorder()
	handler.ServeHTTP(addProxyResult, addProxyReq)
	if addProxyResult.Code != http.StatusCreated {
		t.Fatalf("add proxy failed: %d %s", addProxyResult.Code, addProxyResult.Body.String())
	}
	var aliceProxy Proxy
	if err := json.Unmarshal(addProxyResult.Body.Bytes(), &aliceProxy); err != nil {
		t.Fatal(err)
	}

	adminListReq := httptest.NewRequest(http.MethodGet, "/api/v1/proxies", nil)
	adminListReq.AddCookie(adminCookie)
	adminList := httptest.NewRecorder()
	handler.ServeHTTP(adminList, adminListReq)
	if !bytes.Contains(adminList.Body.Bytes(), []byte(`"proxies":[]`)) {
		t.Fatalf("admin saw another user's proxies: %s", adminList.Body.String())
	}

	aliceListReq := httptest.NewRequest(http.MethodGet, "/api/v1/proxies", nil)
	aliceListReq.AddCookie(aliceCookie)
	aliceList := httptest.NewRecorder()
	handler.ServeHTTP(aliceList, aliceListReq)
	if !bytes.Contains(aliceList.Body.Bytes(), []byte(`"owner":"alice"`)) {
		t.Fatalf("alice proxy missing: %s", aliceList.Body.String())
	}

	adminDeleteProxyReq := httptest.NewRequest(http.MethodDelete, "/api/v1/proxies/"+aliceProxy.ID, nil)
	adminDeleteProxyReq.AddCookie(adminCookie)
	adminDeleteProxy := httptest.NewRecorder()
	handler.ServeHTTP(adminDeleteProxy, adminDeleteProxyReq)
	if adminDeleteProxy.Code != http.StatusNotFound {
		t.Fatalf("admin crossed proxy boundary: %d", adminDeleteProxy.Code)
	}

	deleteUserReq := httptest.NewRequest(http.MethodDelete, "/api/v1/users/alice", nil)
	deleteUserReq.AddCookie(adminCookie)
	deleteUserResult := httptest.NewRecorder()
	handler.ServeHTTP(deleteUserResult, deleteUserReq)
	if deleteUserResult.Code != http.StatusNoContent || len(manager.ListForUser("alice")) != 0 {
		t.Fatalf("delete user failed: %d", deleteUserResult.Code)
	}

	deletedSessionReq := httptest.NewRequest(http.MethodGet, "/api/v1/proxies", nil)
	deletedSessionReq.AddCookie(aliceCookie)
	deletedSessionResult := httptest.NewRecorder()
	handler.ServeHTTP(deletedSessionResult, deletedSessionReq)
	if deletedSessionResult.Code != http.StatusUnauthorized {
		t.Fatalf("deleted user session remained valid: %d", deletedSessionResult.Code)
	}
}
