package vertex

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type MockHandler struct {
	Foo string `schema:"foo" required:"true"`
	Bar string `schema:"bar" required:"true"`
}

func (h MockHandler) Handle(w http.ResponseWriter, r *http.Request) (interface{}, error) {

	return map[string]string{"foo": h.Foo, "bar": h.Bar}, nil
}

const middlewareHeader = "X-Middleware-Message"

func makeMockMW(header string) MiddlewareFunc {

	return MiddlewareFunc(func(w http.ResponseWriter, r *http.Request, next HandlerFunc) (interface{}, error) {
		w.Header().Add(middlewareHeader, header)
		return next(w, r)
	})
}

func testUserHandler(t *TestContext) error {

	req, err := t.NewRequest("GET", nil, nil)
	if err != nil {
		return err
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()

	return nil

}

func TestMiddleware(t *testing.T) {

	mw1 := MiddlewareFunc(func(w http.ResponseWriter, r *http.Request, next HandlerFunc) (interface{}, error) {
		fmt.Fprint(w, "mw1,")
		return next(w, r)
	})

	mw2 := MiddlewareFunc(func(w http.ResponseWriter, r *http.Request, next HandlerFunc) (interface{}, error) {
		fmt.Fprint(w, "mw2,")
		if next != nil {
			return next(w, r)
		}
		return nil, nil

	})

	mw3 := MiddlewareFunc(func(w http.ResponseWriter, r *http.Request, next HandlerFunc) (interface{}, error) {
		fmt.Fprint(w, "mw3")

		return nil, nil

	})
	recorder := httptest.NewRecorder()
	chain := buildChain([]Middleware{mw1, mw2, mw3})

	chain.handle(recorder, nil)
	expected := "mw1,mw2,mw3"
	if recorder.Body.String() != expected {
		t.Errorf("Expected response to be '%s', got '%s'", expected, recorder.Body.String())
	}

}

var mockAPI = &API{
	Root:          "/mock",
	Name:          "testung",
	Version:       "1.0",
	Doc:           "Our fancy testung API",
	Title:         "Testung API!",
	Renderer:      JSONRenderer{},
	AllowInsecure: true,
	Middleware:    []Middleware{makeMockMW("Global middleware")},
	Routes: Routes{
		{
			Path:        "/test",
			Description: "test",
			Handler:     MockHandler{},
			Methods:     GET,
			Middleware:  []Middleware{makeMockMW("Private middleware")},
			Test: WarningTest(func(t *TestContext) {

				req, err := t.NewRequest("GET", url.Values{"foo": []string{"bar"}, "bar": []string{"baz"}}, nil)
				if err != nil {
					t.Fail("Failed creating request %v", err)
				}

				m := map[string]interface{}{}
				_, err = t.JsonRequest(req, &m)
				if err != nil {
					t.Fail("Failed running request %v", err)
				}
				if len(m) == 0 {
					t.Fail("VAlue not serialized")
				}

			}),
		},
		{
			Path:        "/test2",
			Description: "test2",
			Handler: HandlerFunc(func(w http.ResponseWriter, r *http.Request) (interface{}, error) {
				return map[string]string{"YO": "YO"}, nil
			}),
			Methods:    GET,
			Middleware: []Middleware{makeMockMW("Private middleware")},
			Test:       CriticalTest(func(t *TestContext) {}),
		},
		{
			Path:        "/testvoid",
			Description: "testvoid",
			Handler:     VoidHandler{},
			Methods:     GET,
			Middleware:  []Middleware{makeMockMW("Private middleware")},
			Test:        CriticalTest(func(t *TestContext) {}),
		},
	},
}

func TestRegistration(t *testing.T) {

	//t.SkipNow()
	builder := func() *API {
		return mockAPI
	}

	Register("testung", builder, nil)

	if len(apiBuilders) != 1 {
		t.Fatalf("Wrong number of registered APIs: %d", len(apiBuilders))
	}
	srv := NewServer(":9947")
	srv.InitAPIs()
	if len(srv.apis) != 1 {
		t.Errorf("API not registered in server")
	}

}

func TestIntegration(t *testing.T) {
	////t.SkipNow()
	srv := NewServer(":9947")
	srv.AddAPI(mockAPI)

	s := httptest.NewServer(srv.Handler())
	defer s.Close()

	checkRequest := func(u string, tests ...func(r interface{}, hr *http.Response)) {

		res, err := http.Get(u)
		if err != nil {
			t.Errorf("Could not get response data")
		}

		resp := map[string]interface{}{}
		if res.StatusCode == http.StatusOK {
			dec := json.NewDecoder(res.Body)
			if err = dec.Decode(&resp); err != nil {
				t.Errorf("Could not decode json response def: %s. Response code:%v", err, res.Status)
			}

		} else {
			b, _ := ioutil.ReadAll(res.Body)
			t.Log("Response body: %s", string(b))
		}

		res.Body.Close()

		if h, found := res.Header[middlewareHeader]; !found || len(h) == 0 {
			t.Error("Could not fine middleware headers")
		} else {
			if h[0] != "Global middleware" || h[1] != "Private middleware" {
				t.Errorf("Bad m2 injected headers: %s", h)
			}
		}

		for _, f := range tests {
			f(resp, res)
		}

	}

	basicTest := func(resp interface{}, hr *http.Response) {
		if hr.StatusCode != http.StatusOK {
			t.Errorf("bad response code: %d", hr.StatusCode)
		}

		if hr.Header.Get(HeaderProcessingTime) == "" {
			t.Errorf("Bad processing time")
		}

	}

	u := fmt.Sprintf("http://%s%s", s.Listener.Addr().String(), mockAPI.FullPath("/test"))
	u += "?foo=f&bar=b"
	t.Log(u)

	checkRequest(u, basicTest, func(resp interface{}, hr *http.Response) {
		if m, ok := resp.(map[string]interface{}); !ok {
			t.Errorf("Bad response type: %s", reflect.TypeOf(resp))
		} else {

			if m["foo"] != "f" || m["bar"] != "b" {
				t.Errorf("Bad response map: %v", m)
			}

		}
	})

	u = fmt.Sprintf("http://%s%s", s.Listener.Addr().String(), mockAPI.FullPath("/test"))
	checkRequest(u, func(resp interface{}, hr *http.Response) {
		assert.Equal(t, hr.StatusCode, http.StatusBadRequest)
	})

	u = fmt.Sprintf("http://%s%s", s.Listener.Addr().String(), mockAPI.FullPath("/test2"))

	checkRequest(u, basicTest, func(resp interface{}, hr *http.Response) {
		if m, ok := resp.(map[string]interface{}); !ok {
			t.Errorf("Bad response type: %s", reflect.TypeOf(resp))
		} else {

			if m["YO"] != "YO" {
				t.Errorf("Bad response: %v", m)
			}

		}
	})

	u = fmt.Sprintf("http://%s%s", s.Listener.Addr().String(), mockAPI.FullPath("/testvoid"))
	checkRequest(u, basicTest, func(resp interface{}, hr *http.Response) {
		if m, ok := resp.(map[string]interface{}); !ok {
			t.Errorf("Bad response type: %s", reflect.TypeOf(resp))
		} else {
			if len(m) != 0 {
				t.Error("Expected empty map, got", m)
			}
		}
	})
	// Test integration tests

	u = fmt.Sprintf("http://%s/test/%s/warning", s.Listener.Addr().String(), mockAPI.root())

	res, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(b), "[PASS]") {
		t.Errorf("API tests did not pass")
		t.Log(string(b))
	}
	if !strings.Contains(string(b), "warning") {
		t.Errorf("API tests did not contain critical")
	}
	res.Body.Close()

	u = fmt.Sprintf("http://%s/test/%s/critical", s.Listener.Addr().String(), mockAPI.root())

	if res, err = http.Get(u); err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if b, err = ioutil.ReadAll(res.Body); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(b), "[PASS]") {
		t.Errorf("API tests did not pass")
	}
	if !strings.Contains(string(b), "critical") {
		t.Errorf("API tests did not contain critical")
	}

}

func TestFormatPath(t *testing.T) {
	//t.SkipNow()
	data := []struct {
		path     string
		params   Params
		expected string
	}{
		{"/foo/{bar}", Params{"bar": "baz", "baz": "foo"}, "/foo/baz"},
		{"/foo/{bar}", nil, "/foo/{bar}"},
		{"/foo/{biz}", Params{"bar": "baz", "baz": "foo"}, "/foo/{biz}"},
	}

	for _, x := range data {
		out := FormatPath(x.path, x.params)
		if out != x.expected {
			t.Errorf("Bad formatting. expected '%s', got '%s'", x.expected, out)
		}
	}

}

func TestIsHijacked(t *testing.T) {

	assert.False(t, IsHijacked(nil))
	assert.False(t, IsHijacked(errors.New("Foo")))
	assert.True(t, IsHijacked(NewErrorCode("hijacked", Hijacked)))
	assert.True(t, IsHijacked(ErrHijacked))
}

func TestRenderer(t *testing.T) {

	r := RenderFunc(func(resp *response, w http.ResponseWriter, r *http.Request) error {
		fmt.Fprintln(w, "testung")
		return nil
	}, "text/plain")

	assert.EqualValues(t, r.ContentTypes(), []string{"text/plain"})
	out := httptest.NewRecorder()

	err := r.Render(nil, out, nil)
	if err != nil {
		t.Error(err)
	}
	out.Flush()

	assert.Equal(t, "testung\n", out.Body.String())

	// test json renderer

	jr := JSONRenderer{}
	out = httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "http://foo.bar?callback=foo", nil)
	req.ParseForm()
	resp := response{
		ErrorString:    "OK",
		ErrorCode:      1,
		ProcessingTime: 1,
		RequestId:      "sfsgds",
		ResponseObject: "ello",
	}

	assert.NoError(t, jr.Render(&resp, out, req))
	assert.Equal(t, `"ello"`, out.Body.String())

	out = httptest.NewRecorder()
	writeError(out, "watwat")
	assert.Equal(t, "watwat\n", out.Body.String())

}

const mockConfs = `
server:
  listen: :8686
apis:
  testung:
     foo: baz
`

func TestConfigs(t *testing.T) {

	var apiConf = struct {
		Foo string `yaml:"foo"`
	}{"not baz"}

	registerAPIConfig("testung", &apiConf)

	confile := "/tmp/apiconf.test.yaml"
	fp, err := os.Create(confile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fp.WriteString(mockConfs); err != nil {
		t.Fatal(err)
	}
	fp.Close()

	flag.Set("conf", confile)
	assert.NoError(t, ReadConfigs())
	assert.Equal(t, Config.Server.ListenAddr, ":8686")
	assert.Equal(t, "baz", apiConf.Foo)
}

func TestErrors(t *testing.T) {

	err := NewError("wat")
	if e, ok := err.(*internalError); !ok {
		t.Fatal("returned not an internal error")
	} else {
		assert.Equal(t, e.Code, GeneralFailure)
		assert.Equal(t, e.Message, "wat")
	}

	err = NewErrorCode("word", Unauthorized)
	if e, ok := err.(*internalError); !ok {
		t.Fatal("returned not an internal error")
	} else {
		assert.Equal(t, e.Code, Unauthorized)
		assert.Equal(t, e.Message, "word")

		assert.Equal(t, http.StatusUnauthorized, httpCode(e.Code))
	}

	err = NewErrorf("word %s", "dawg")
	if e, ok := err.(*internalError); !ok {
		t.Fatal("returned not an internal error")
	} else {
		assert.Equal(t, e.Code, GeneralFailure)
		assert.Equal(t, e.Message, "word dawg")

		assert.Equal(t, http.StatusInternalServerError, httpCode(e.Code))
	}

}

func TestServer(t *testing.T) {

	s := NewServer(":9934")

	s.AddAPI(mockAPI)

	go func() {
		if err := s.Run(); err != nil {
			t.Fatal(err)
		}
	}()

	time.Sleep(100 * time.Millisecond)

	s.Stop()

}
