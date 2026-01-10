package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
)

func TestNPMClient_FetchVersions(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		want       []string
		wantErr    bool
		errType    interface{}
	}{
		{
			name: "successful fetch",
			handler: func(w http.ResponseWriter, r *http.Request) {
				info := NPMPackageInfo{
					Name: "test-package",
					Versions: map[string]struct{}{
						"1.0.0": {},
						"1.0.1": {},
						"2.0.0": {},
					},
				}
				json.NewEncoder(w).Encode(info)
			},
			want: []string{"1.0.0", "1.0.1", "2.0.0"},
		},
		{
			name: "package not found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte("package not found"))
			},
			wantErr: true,
			errType: &RegistryError{},
		},
		{
			name: "invalid json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("not valid json"))
			},
			wantErr: true,
			errType: &NetworkError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			client := NewNPMClient(WithBaseURL(srv.URL))
			got, err := client.FetchVersions(context.Background(), "test-package")

			if (err != nil) != tt.wantErr {
				t.Errorf("FetchVersions() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.errType != nil {
					gotType := reflect.TypeOf(err)
					wantType := reflect.TypeOf(tt.errType)
					if gotType != wantType {
						t.Errorf("FetchVersions() error type = %v, want %v", gotType, wantType)
					}
				}
				return
			}

			// Sort for comparison since map iteration order is non-deterministic
			sort.Strings(got)
			sort.Strings(tt.want)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FetchVersions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNPMClient_FetchDistTags(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    DistTags
		wantErr bool
	}{
		{
			name: "successful fetch",
			handler: func(w http.ResponseWriter, r *http.Request) {
				info := NPMPackageInfo{
					Name: "test-package",
					DistTags: DistTags{
						"latest": "2.0.0",
						"stable": "1.5.0",
						"next":   "3.0.0-beta",
					},
				}
				json.NewEncoder(w).Encode(info)
			},
			want: DistTags{
				"latest": "2.0.0",
				"stable": "1.5.0",
				"next":   "3.0.0-beta",
			},
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("internal error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			client := NewNPMClient(WithBaseURL(srv.URL))
			got, err := client.FetchDistTags(context.Background(), "test-package")

			if (err != nil) != tt.wantErr {
				t.Errorf("FetchDistTags() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FetchDistTags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNPMClient_Options(t *testing.T) {
	t.Run("default options", func(t *testing.T) {
		client := NewNPMClient()
		if client.baseURL != defaultNPMRegistry {
			t.Errorf("baseURL = %v, want %v", client.baseURL, defaultNPMRegistry)
		}
		if client.timeout != defaultTimeout {
			t.Errorf("timeout = %v, want %v", client.timeout, defaultTimeout)
		}
	})

	t.Run("custom options", func(t *testing.T) {
		customClient := &http.Client{}
		customURL := "https://custom.registry.com"

		client := NewNPMClient(
			WithHTTPClient(customClient),
			WithBaseURL(customURL),
		)

		if client.httpClient != customClient {
			t.Error("httpClient was not set correctly")
		}
		if client.baseURL != customURL {
			t.Errorf("baseURL = %v, want %v", client.baseURL, customURL)
		}
	})
}

func TestNPMClient_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		<-r.Context().Done()
	}))
	defer srv.Close()

	client := NewNPMClient(WithBaseURL(srv.URL))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.FetchVersions(ctx, "test-package")
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}
