package kb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeEmbeddingServer returns deterministic vectors: embedding[i] = float32(len(text)+i).
func fakeEmbeddingServer(t *testing.T, model string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing bearer auth")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req embeddingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Model != model {
			t.Errorf("want model %q, got %q", model, req.Model)
		}
		resp := embeddingsResponse{}
		for i, text := range req.Input {
			v := make([]float32, 4)
			for j := range v {
				v[j] = float32(len(text) + i + j)
			}
			resp.Data = append(resp.Data, struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			}{Index: i, Embedding: v})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestOpenAIEmbedderRoundTrip(t *testing.T) {
	srv := fakeEmbeddingServer(t, "test-model")
	defer srv.Close()
	emb := NewOpenAIEmbedder("test-key", srv.URL, "test-model")
	vecs, err := emb.Embed(context.Background(), []string{"hello", "world!"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("want 2 vectors, got %d", len(vecs))
	}
	if len(vecs[0]) != 4 || vecs[0][0] != float32(len("hello")) {
		t.Fatalf("unexpected vector content: %v", vecs[0])
	}
	if emb.Model() != "test-model" {
		t.Fatalf("want model test-model, got %q", emb.Model())
	}
}

func TestOpenAIEmbedderBatches(t *testing.T) {
	srv := fakeEmbeddingServer(t, "m")
	defer srv.Close()
	emb := NewOpenAIEmbedder("test-key", srv.URL, "m")
	texts := make([]string, batchSize+10)
	for i := range texts {
		texts[i] = "text"
	}
	vecs, err := emb.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("want %d vectors, got %d", len(texts), len(vecs))
	}
}

func TestOpenAIEmbedderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad key"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	emb := NewOpenAIEmbedder("bad", srv.URL, "m")
	if _, err := emb.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("want error on 401, got nil")
	}
}

func TestOpenAIEmbedderEmpty(t *testing.T) {
	emb := NewOpenAIEmbedder("k", "http://localhost:1", "m")
	vecs, err := emb.Embed(context.Background(), nil)
	if err != nil || vecs != nil {
		t.Fatalf("want nil,nil for empty input, got %v,%v", vecs, err)
	}
}
