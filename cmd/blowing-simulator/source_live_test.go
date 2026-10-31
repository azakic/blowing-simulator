package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestLiveSourceStore proverava pravi OCI bucket: PUT → presigned GET → DELETE.
// Podrazumevano se preskače; pokreće se sa S3_LIVE_TEST=1 i S3_* env
// promenljivama (isti Secret koji koristi deployment).
func TestLiveSourceStore(t *testing.T) {
	if os.Getenv("S3_LIVE_TEST") != "1" {
		t.Skip("S3_LIVE_TEST != 1 — preskačem live S3 test")
	}

	store := NewSourceStoreFromEnv()
	if !store.Enabled() {
		t.Fatal("store nije omogućen — nedostaje S3_* env")
	}

	ctx := context.Background()
	data := []byte("%PDF-1.4\n% live test " + time.Now().UTC().Format(time.RFC3339Nano) + "\n")
	sha := sourceSHA256(data)
	key := store.objectKey(sha)

	defer func() {
		_, _ = store.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(store.bucket),
			Key:    aws.String(key),
		})
	}()

	if err := store.PutIfAbsent(ctx, sha, data); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	// Drugi put mora biti no-op (dedupe po hash-u).
	if err := store.PutIfAbsent(ctx, sha, data); err != nil {
		t.Fatalf("PutIfAbsent (ponovo): %v", err)
	}

	url, err := store.PresignGet(ctx, sha, "test \"protocol\".pdf", 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET presigned: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("presigned GET -> %d: %s", resp.StatusCode, body)
	}

	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(data) {
		t.Fatalf("sadržaj se ne poklapa (dobijeno %d, poslato %d bajtova)", len(got), len(data))
	}

	disp := resp.Header.Get("Content-Disposition")
	t.Logf("ok: %d bajtova, key=%s, content-disposition=%q", len(got), key, disp)
}
