package http

import (
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/rqlite/rqlite/command"
	"github.com/rqlite/rqlite/store"
	"github.com/rqlite/rqlite/testdata/x509"

	"golang.org/x/net/http2"
)

func Test_NormalizeAddr(t *testing.T) {
	tests := []struct {
		orig string
		norm string
	}{
		{
			orig: "http://localhost:4001",
			norm: "http://localhost:4001",
		},
		{
			orig: "https://localhost:4001",
			norm: "https://localhost:4001",
		},
		{
			orig: "https://localhost:4001/foo",
			norm: "https://localhost:4001/foo",
		},
		{
			orig: "localhost:4001",
			norm: "http://localhost:4001",
		},
		{
			orig: "localhost",
			norm: "http://localhost",
		},
		{
			orig: ":4001",
			norm: "http://:4001",
		},
	}

	for _, tt := range tests {
		if NormalizeAddr(tt.orig) != tt.norm {
			t.Fatalf("%s not normalized correctly, got: %s", tt.orig, tt.norm)
		}
	}
}

func Test_EnsureHTTPS(t *testing.T) {
	tests := []struct {
		orig    string
		ensured string
	}{
		{
			orig:    "http://localhost:4001",
			ensured: "https://localhost:4001",
		},
		{
			orig:    "https://localhost:4001",
			ensured: "https://localhost:4001",
		},
		{
			orig:    "https://localhost:4001/foo",
			ensured: "https://localhost:4001/foo",
		},
		{
			orig:    "localhost:4001",
			ensured: "https://localhost:4001",
		},
	}

	for _, tt := range tests {
		if e := EnsureHTTPS(tt.orig); e != tt.ensured {
			t.Fatalf("%s not HTTPS ensured correctly, exp %s, got %s", tt.orig, tt.ensured, e)
		}
	}
}

func Test_NewService(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	if s == nil {
		t.Fatalf("failed to create new service")
	}
}

func Test_HasVersionHeader(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()
	s.BuildInfo = map[string]interface{}{
		"version": "the version",
	}
	url := fmt.Sprintf("http://%s", s.Addr().String())

	client := &http.Client{}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("failed to make request")
	}

	if resp.Header.Get("X-RQLITE-VERSION") != "the version" {
		t.Fatalf("incorrect build version present in HTTP response header")
	}
}

func Test_HasContentTypeJSON(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()

	client := &http.Client{}
	resp, err := client.Get(fmt.Sprintf("http://%s/status", s.Addr().String()))
	if err != nil {
		t.Fatalf("failed to make request")
	}

	h := resp.Header.Get("Content-Type")
	if h != "application/json; charset=utf-8" {
		t.Fatalf("incorrect Content-type in HTTP response: %s", h)
	}
}

func Test_HasContentTypeOctetStream(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()

	client := &http.Client{}
	resp, err := client.Get(fmt.Sprintf("http://%s/db/backup", s.Addr().String()))
	if err != nil {
		t.Fatalf("failed to make request")
	}

	h := resp.Header.Get("Content-Type")
	if h != "application/octet-stream" {
		t.Fatalf("incorrect Content-type in HTTP response: %s", h)
	}
}

func Test_HasVersionHeaderUnknown(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()
	url := fmt.Sprintf("http://%s", s.Addr().String())

	client := &http.Client{}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("failed to make request")
	}

	if resp.Header.Get("X-RQLITE-VERSION") != "unknown" {
		t.Fatalf("incorrect build version present in HTTP response header")
	}
}

func Test_404Routes(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()
	host := fmt.Sprintf("http://%s", s.Addr().String())

	client := &http.Client{}

	resp, err := client.Get(host + "/db/xxx")
	if err != nil {
		t.Fatalf("failed to make request")
	}
	if resp.StatusCode != 404 {
		t.Fatalf("failed to get expected 404, got %d", resp.StatusCode)
	}

	resp, err = client.Post(host+"/xxx", "", nil)
	if err != nil {
		t.Fatalf("failed to make request")
	}
	if resp.StatusCode != 404 {
		t.Fatalf("failed to get expected 404, got %d", resp.StatusCode)
	}
}

func Test_404Routes_ExpvarPprofDisabled(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()
	host := fmt.Sprintf("http://%s", s.Addr().String())

	client := &http.Client{}

	for _, path := range []string{
		"/debug/vars",
		"/debug/pprof/cmdline",
		"/debug/pprof/profile",
		"/debug/pprof/symbol",
	} {
		req, err := http.NewRequest("GET", host+path, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %s", err.Error())
		}
		if resp.StatusCode != 404 {
			t.Fatalf("failed to get expected 404 for path %s, got %d", path, resp.StatusCode)
		}
	}
}

func Test_405Routes(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()
	host := fmt.Sprintf("http://%s", s.Addr().String())

	client := &http.Client{}

	resp, err := client.Get(host + "/db/execute")
	if err != nil {
		t.Fatalf("failed to make request")
	}
	if resp.StatusCode != 405 {
		t.Fatalf("failed to get expected 405, got %d", resp.StatusCode)
	}

	resp, err = client.Get(host + "/remove")
	if err != nil {
		t.Fatalf("failed to make request")
	}
	if resp.StatusCode != 405 {
		t.Fatalf("failed to get expected 405, got %d", resp.StatusCode)
	}

	resp, err = client.Post(host+"/remove", "", nil)
	if err != nil {
		t.Fatalf("failed to make request")
	}
	if resp.StatusCode != 405 {
		t.Fatalf("failed to get expected 405, got %d", resp.StatusCode)
	}

	resp, err = client.Get(host + "/join")
	if err != nil {
		t.Fatalf("failed to make request")
	}
	if resp.StatusCode != 405 {
		t.Fatalf("failed to get expected 405, got %d", resp.StatusCode)
	}

	resp, err = client.Post(host+"/db/backup", "", nil)
	if err != nil {
		t.Fatalf("failed to make request")
	}
	if resp.StatusCode != 405 {
		t.Fatalf("failed to get expected 405, got %d", resp.StatusCode)
	}

	resp, err = client.Post(host+"/status", "", nil)
	if err != nil {
		t.Fatalf("failed to make request")
	}
	if resp.StatusCode != 405 {
		t.Fatalf("failed to get expected 405, got %d", resp.StatusCode)
	}
}

func Test_400Routes(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()
	host := fmt.Sprintf("http://%s", s.Addr().String())

	client := &http.Client{}

	resp, err := client.Get(host + "/db/query?q=")
	if err != nil {
		t.Fatalf("failed to make request")
	}
	if resp.StatusCode != 400 {
		t.Fatalf("failed to get expected 400, got %d", resp.StatusCode)
	}
}

func Test_401Routes_NoBasicAuth(t *testing.T) {
	c := &mockCredentialStore{CheckOK: false, HasPermOK: false}

	m := &MockStore{}
	n := &mockClusterService{}
	s := New("127.0.0.1:0", m, n, c)
	s.Expvar = true
	s.Pprof = true
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()
	host := fmt.Sprintf("http://%s", s.Addr().String())

	client := &http.Client{}

	for _, path := range []string{
		"/db/execute",
		"/db/query",
		"/db/backup",
		"/db/load",
		"/join",
		"/delete",
		"/status",
		"/nodes",
		"/debug/vars",
		"/debug/pprof/cmdline",
		"/debug/pprof/profile",
		"/debug/pprof/symbol",
	} {
		resp, err := client.Get(host + path)
		if err != nil {
			t.Fatalf("failed to make request")
		}
		if resp.StatusCode != 401 {
			t.Fatalf("failed to get expected 401 for path %s, got %d", path, resp.StatusCode)
		}
	}
}

func Test_401Routes_BasicAuthBadPassword(t *testing.T) {
	c := &mockCredentialStore{CheckOK: false, HasPermOK: false}

	m := &MockStore{}
	n := &mockClusterService{}
	s := New("127.0.0.1:0", m, n, c)
	s.Expvar = true
	s.Pprof = true
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()
	host := fmt.Sprintf("http://%s", s.Addr().String())

	client := &http.Client{}

	for _, path := range []string{
		"/db/execute",
		"/db/query",
		"/db/backup",
		"/db/load",
		"/join",
		"/status",
		"/nodes",
		"/debug/vars",
		"/debug/pprof/cmdline",
		"/debug/pprof/profile",
		"/debug/pprof/symbol",
	} {
		req, err := http.NewRequest("GET", host+path, nil)
		if err != nil {
			t.Fatalf("failed to create request: %s", err.Error())
		}
		req.SetBasicAuth("username1", "password1")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %s", err.Error())
		}
		if resp.StatusCode != 401 {
			t.Fatalf("failed to get expected 401 for path %s, got %d", path, resp.StatusCode)
		}
	}
}

func Test_401Routes_BasicAuthBadPerm(t *testing.T) {
	c := &mockCredentialStore{CheckOK: true, HasPermOK: false}

	m := &MockStore{}
	n := &mockClusterService{}
	s := New("127.0.0.1:0", m, n, c)
	s.Expvar = true
	s.Pprof = true
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()
	host := fmt.Sprintf("http://%s", s.Addr().String())

	client := &http.Client{}

	for _, path := range []string{
		"/db/execute",
		"/db/query",
		"/db/backup",
		"/db/load",
		"/join",
		"/status",
		"/debug/vars",
		"/debug/pprof/cmdline",
		"/debug/pprof/profile",
		"/debug/pprof/symbol",
	} {
		req, err := http.NewRequest("GET", host+path, nil)
		if err != nil {
			t.Fatalf("failed to create request: %s", err.Error())
		}
		req.SetBasicAuth("username1", "password1")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %s", err.Error())
		}
		if resp.StatusCode != 401 {
			t.Fatalf("failed to get expected 401 for path %s, got %d", path, resp.StatusCode)
		}
	}
}

func Test_BackupOK(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()

	m.backupFn = func(leader bool, f store.BackupFormat, dst io.Writer) error {
		return nil
	}

	client := &http.Client{}
	host := fmt.Sprintf("http://%s", s.Addr().String())
	resp, err := client.Get(host + "/db/backup")
	if err != nil {
		t.Fatalf("failed to make backup request")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to get expected StatusOK for backup, got %d", resp.StatusCode)
	}
}

func Test_BackupFlagsNoLeader(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{
		apiAddr: "http://1.2.3.4:999",
	}

	s := New("127.0.0.1:0", m, c, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()

	m.backupFn = func(leader bool, f store.BackupFormat, dst io.Writer) error {
		return store.ErrNotLeader
	}

	client := &http.Client{}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	host := fmt.Sprintf("http://%s", s.Addr().String())
	resp, err := client.Get(host + "/db/backup")
	if err != nil {
		t.Fatalf("failed to make backup request: %s", err.Error())
	}
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("failed to get expected StatusServiceUnavailable for backup, got %d", resp.StatusCode)
	}
}

func Test_BackupFlagsNoLeaderOK(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{
		apiAddr: "http://1.2.3.4:999",
	}

	s := New("127.0.0.1:0", m, c, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()

	m.backupFn = func(leader bool, f store.BackupFormat, dst io.Writer) error {
		if !leader {
			return nil
		}
		return store.ErrNotLeader
	}

	client := &http.Client{}
	host := fmt.Sprintf("http://%s", s.Addr().String())
	resp, err := client.Get(host + "/db/backup?noleader")
	if err != nil {
		t.Fatalf("failed to make backup request")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to get expected StatusOK for backup, got %d", resp.StatusCode)
	}
}

func Test_RegisterStatus(t *testing.T) {
	var stats *mockStatuser
	m := &MockStore{}
	c := &mockClusterService{}

	s := New("127.0.0.1:0", m, c, nil)

	if err := s.RegisterStatus("foo", stats); err != nil {
		t.Fatalf("failed to register statuser: %s", err.Error())
	}

	if err := s.RegisterStatus("foo", stats); err == nil {
		t.Fatal("successfully re-registered statuser")
	}
}

func Test_FormRedirect(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}

	s := New("127.0.0.1:0", m, c, nil)
	req := mustNewHTTPRequest("http://qux:4001")

	if rd := s.FormRedirect(req, "http://foo:4001"); rd != "http://foo:4001" {
		t.Fatal("failed to form redirect for simple URL")
	}
}

func Test_FormRedirectParam(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	req := mustNewHTTPRequest("http://qux:4001/db/query?x=y")

	if rd := s.FormRedirect(req, "http://foo:4001"); rd != "http://foo:4001/db/query?x=y" {
		t.Fatal("failed to form redirect for URL")
	}
}

func Test_FormRedirectHTTPS(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	s := New("127.0.0.1:0", m, c, nil)
	req := mustNewHTTPRequest("http://qux:4001")

	if rd := s.FormRedirect(req, "https://foo:4001"); rd != "https://foo:4001" {
		t.Fatal("failed to form redirect for simple URL")
	}
}

func Test_Nodes(t *testing.T) {
	m := &MockStore{
		leaderAddr: "foo:1234",
	}
	c := &mockClusterService{
		apiAddr: "https://bar:5678",
	}
	s := New("127.0.0.1:0", m, c, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()

	client := &http.Client{}
	host := fmt.Sprintf("http://%s", s.Addr().String())
	resp, err := client.Get(host + "/nodes")
	if err != nil {
		t.Fatalf("failed to make nodes request")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to get expected StatusOK for nodes, got %d", resp.StatusCode)
	}
}

func Test_TLSServce(t *testing.T) {
	m := &MockStore{}
	c := &mockClusterService{}
	var s *Service
	tempDir := mustTempDir()

	s = New("127.0.0.1:0", m, c, nil)
	s.CertFile = x509.CertFile(tempDir)
	s.KeyFile = x509.KeyFile(tempDir)
	s.BuildInfo = map[string]interface{}{
		"version": "the version",
	}
	if err := s.Start(); err != nil {
		t.Fatalf("failed to start service")
	}
	defer s.Close()

	url := fmt.Sprintf("https://%s", s.Addr().String())

	// Test connecting with a HTTP client.
	tn := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tn}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("failed to make HTTP request: %s", err)
	}

	if v := resp.Header.Get("X-RQLITE-VERSION"); v != "the version" {
		t.Fatalf("incorrect build version present in HTTP response header, got: %s", v)
	}

	// Test connecting with a HTTP/2 client.
	client = &http.Client{
		Transport: &http2.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err = client.Get(url)
	if err != nil {
		t.Fatalf("failed to make HTTP/2 request: %s", err)
	}

	if v := resp.Header.Get("X-RQLITE-VERSION"); v != "the version" {
		t.Fatalf("incorrect build version present in HTTP/2 response header, got: %s", v)
	}
}

func Test_timeoutQueryParam(t *testing.T) {
	var req http.Request

	defStr := "10s"
	def := mustParseDuration(defStr)
	tests := []struct {
		u   string
		dur string
	}{
		{
			u:   "http://localhost:4001/nodes?timeout=5s",
			dur: "5s",
		},
		{
			u:   "http://localhost:4001/nodes?timeout=2m",
			dur: "2m",
		},
		{
			u:   "http://localhost:4001/nodes?x=777&timeout=5s",
			dur: "5s",
		},
		{
			u:   "http://localhost:4001/nodes",
			dur: defStr,
		},
		{
			u:   "http://localhost:4001/nodes?timeout=zdfjkh",
			dur: defStr,
		},
	}

	for _, tt := range tests {
		req.URL = mustURLParse(tt.u)
		timeout, err := timeout(&req, def)
		if err != nil {
			t.Fatalf("failed to get timeout: %s", err)
		}
		if timeout != mustParseDuration(tt.dur) {
			t.Fatalf("got wrong timeout, expected %s, got %s", mustParseDuration(tt.dur), timeout)
		}
	}
}

type MockStore struct {
	executeFn  func(queries []string, tx bool) ([]*command.ExecuteResult, error)
	queryFn    func(queries []string, tx, leader, verify bool) ([]*command.QueryRows, error)
	backupFn   func(leader bool, f store.BackupFormat, dst io.Writer) error
	leaderAddr string
}

func (m *MockStore) Execute(er *command.ExecuteRequest) ([]*command.ExecuteResult, error) {
	if m.executeFn == nil {
		return nil, nil
	}
	return nil, nil
}

func (m *MockStore) Query(qr *command.QueryRequest) ([]*command.QueryRows, error) {
	if m.queryFn == nil {
		return nil, nil
	}
	return nil, nil
}

func (m *MockStore) Join(id, addr string, voter bool) error {
	return nil
}

func (m *MockStore) Remove(id string) error {
	return nil
}

func (m *MockStore) LeaderAddr() (string, error) {
	return m.leaderAddr, nil
}

func (m *MockStore) Stats() (map[string]interface{}, error) {
	return nil, nil
}

func (m *MockStore) Nodes() ([]*store.Server, error) {
	return nil, nil
}

func (m *MockStore) Backup(leader bool, f store.BackupFormat, w io.Writer) error {
	if m.backupFn == nil {
		return nil
	}
	return m.backupFn(leader, f, w)
}

type mockClusterService struct {
	apiAddr string
}

func (m *mockClusterService) GetNodeAPIAddr(a string) (string, error) {
	return m.apiAddr, nil
}

type mockCredentialStore struct {
	CheckOK   bool
	HasPermOK bool
}

func (m *mockCredentialStore) Check(username, password string) bool {
	return m.CheckOK
}

func (m *mockCredentialStore) HasPerm(username, perm string) bool {
	return m.HasPermOK
}

func (m *mockClusterService) Stats() (map[string]interface{}, error) {
	return nil, nil
}

func (m *mockCredentialStore) HasAnyPerm(username string, perm ...string) bool {
	return m.HasPermOK
}

type mockStatuser struct {
}

func (m *mockStatuser) Stats() (interface{}, error) {
	return nil, nil
}

func mustNewHTTPRequest(url string) *http.Request {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		panic("failed to create HTTP request for testing")
	}
	return req
}

func mustTempDir() string {
	var err error
	path, err := ioutil.TempDir("", "rqlilte-system-test-")
	if err != nil {
		panic("failed to create temp dir")
	}
	return path
}

func mustURLParse(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic("failed to URL parse string")
	}
	return u
}

func mustParseDuration(d string) time.Duration {
	if dur, err := time.ParseDuration(d); err != nil {
		panic("failed to parse duration")
	} else {
		return dur
	}
}

func mustReadResponseBody(resp *http.Response) string {
	response, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic("failed to ReadAll response body")
	}
	resp.Body.Close()
	return string(response)
}
