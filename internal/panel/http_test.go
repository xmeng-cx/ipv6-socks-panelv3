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
	cfg.MaxProxies = 20
	cfg.HY2ObfsPassword = "5201314"
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
	var createdUser struct {
		ProxyCount      int    `json:"proxyCount"`
		SubscriptionURL string `json:"subscriptionUrl"`
	}
	if err := json.Unmarshal(addUserResult.Body.Bytes(), &createdUser); err != nil {
		t.Fatal(err)
	}
	if createdUser.ProxyCount != 10 || createdUser.SubscriptionURL == "" {
		t.Fatalf("new user defaults missing: %#v", createdUser)
	}
	subscriptionReq := httptest.NewRequest(http.MethodGet, createdUser.SubscriptionURL, nil)
	subscriptionResult := httptest.NewRecorder()
	handler.ServeHTTP(subscriptionResult, subscriptionReq)
	if subscriptionResult.Code != http.StatusOK || !bytes.Contains(subscriptionResult.Body.Bytes(), []byte("mode: global")) || bytes.Count(subscriptionResult.Body.Bytes(), []byte("type: hysteria2")) != 10 {
		t.Fatalf("invalid Mihomo subscription: %d %s", subscriptionResult.Code, subscriptionResult.Body.String())
	}

	aliceCookie := loginCookie(t, handler, "alice", "alice123")
	aliceHealthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	aliceHealthReq.AddCookie(aliceCookie)
	aliceHealth := httptest.NewRecorder()
	handler.ServeHTTP(aliceHealth, aliceHealthReq)
	if bytes.Contains(aliceHealth.Body.Bytes(), []byte(`"network"`)) || bytes.Contains(aliceHealth.Body.Bytes(), []byte(`"totalProxyCount"`)) {
		t.Fatalf("ordinary user received network information: %s", aliceHealth.Body.String())
	}
	aliceSettingsReq := httptest.NewRequest(http.MethodPut, "/api/v1/subscription/settings", bytes.NewBufferString(`{"directRules":["example.com"]}`))
	aliceSettingsReq.AddCookie(aliceCookie)
	aliceSettings := httptest.NewRecorder()
	handler.ServeHTTP(aliceSettings, aliceSettingsReq)
	if aliceSettings.Code != http.StatusForbidden {
		t.Fatalf("ordinary user changed direct settings: %d", aliceSettings.Code)
	}
	adminSettingsReq := httptest.NewRequest(http.MethodPut, "/api/v1/subscription/settings", bytes.NewBufferString(`{"directRules":["example.com","1.1.1.1"]}`))
	adminSettingsReq.AddCookie(adminCookie)
	adminSettings := httptest.NewRecorder()
	handler.ServeHTTP(adminSettings, adminSettingsReq)
	if adminSettings.Code != http.StatusOK {
		t.Fatalf("admin direct settings failed: %d %s", adminSettings.Code, adminSettings.Body.String())
	}
	subscriptionWithRules := httptest.NewRecorder()
	handler.ServeHTTP(subscriptionWithRules, httptest.NewRequest(http.MethodGet, createdUser.SubscriptionURL, nil))
	if !bytes.Contains(subscriptionWithRules.Body.Bytes(), []byte("DOMAIN-SUFFIX,example.com,DIRECT")) || !bytes.Contains(subscriptionWithRules.Body.Bytes(), []byte("IP-CIDR,1.1.1.1/32,DIRECT,no-resolve")) {
		t.Fatalf("direct settings missing from subscription: %s", subscriptionWithRules.Body.String())
	}
	userAddProxyReq := httptest.NewRequest(http.MethodPost, "/api/v1/proxies", bytes.NewBufferString(`{"protocol":"socks5"}`))
	userAddProxyReq.AddCookie(aliceCookie)
	userAddProxyResult := httptest.NewRecorder()
	handler.ServeHTTP(userAddProxyResult, userAddProxyReq)
	if userAddProxyResult.Code != http.StatusForbidden {
		t.Fatalf("ordinary user created a proxy: %d %s", userAddProxyResult.Code, userAddProxyResult.Body.String())
	}
	addProxyReq := httptest.NewRequest(http.MethodPost, "/api/v1/proxies", bytes.NewBufferString(`{"protocol":"socks5","owner":"alice","count":2}`))
	addProxyReq.AddCookie(adminCookie)
	addProxyResult := httptest.NewRecorder()
	handler.ServeHTTP(addProxyResult, addProxyReq)
	if addProxyResult.Code != http.StatusCreated {
		t.Fatalf("admin add proxy failed: %d %s", addProxyResult.Code, addProxyResult.Body.String())
	}
	var batch struct {
		Proxies []Proxy `json:"proxies"`
		Count   int     `json:"count"`
	}
	if err := json.Unmarshal(addProxyResult.Body.Bytes(), &batch); err != nil {
		t.Fatal(err)
	}
	if batch.Count != 2 || len(batch.Proxies) != 2 {
		t.Fatalf("unexpected batch response: %#v", batch)
	}
	aliceProxy := batch.Proxies[0]

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
	if len(manager.ListForUser("alice")) != 12 {
		t.Fatalf("expected 10 default and two admin-created lines, got %d", len(manager.ListForUser("alice")))
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
