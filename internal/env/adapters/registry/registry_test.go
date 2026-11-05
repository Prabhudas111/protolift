// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := map[string]Ref{
		"postgres":                          {dockerHub, "library/postgres", "latest"},
		"postgres:16":                       {dockerHub, "library/postgres", "16"},
		"localstack/localstack:4":           {dockerHub, "localstack/localstack", "4"},
		"ghcr.io/acme/web:1.2":              {"ghcr.io", "acme/web", "1.2"},
		"localhost:5000/app":                {"localhost:5000", "app", "latest"},
		"docker.io/library/redis@sha256:ab": {dockerHub, "library/redis", "sha256:ab"},
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Fatalf("Parse(%q) = %+v, %v. Want %+v", in, got, err, want)
		}
	}
	if _, err := Parse("${IMAGE}:dev"); err == nil {
		t.Fatal("a variable image must not parse")
	}
}

func TestSupportIndexWithTokenChallenge(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			w.Write([]byte(`{"token":"t1"}`))
		case r.Header.Get("Authorization") != "Bearer t1":
			w.Header().Set("Www-Authenticate", `Bearer realm="`+srv.URL+`/token",service="reg",scope="repository:acme/web:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.Write([]byte(`{"manifests":[{"platform":{"architecture":"amd64","os":"linux"}},{"platform":{"architecture":"arm64","os":"linux"}},{"platform":{"architecture":"unknown","os":"unknown"}}]}`))
		}
	}))
	defer srv.Close()
	in := &Inspector{c: srv.Client()}
	host := strings.TrimPrefix(srv.URL, "https://")
	s := in.Support(context.Background(), host+"/acme/web:1")
	if !s.Known || !s.ARM || !s.Intel {
		t.Fatalf("support = %+v", s)
	}
}

func TestSupportUnknownOnFailure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	in := &Inspector{c: srv.Client()}
	if s := in.Support(context.Background(), strings.TrimPrefix(srv.URL, "https://")+"/x/y:1"); s.Known {
		t.Fatalf("a failed lookup must be unknown, got %+v", s)
	}
}
