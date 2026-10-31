package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jmoiron/sqlx"
)

// SourceStore čuva originalne PDF-ove u OCI Object Storage (S3 API).
//
// Originali se do 2026-10-31 nisu čuvali: PDF se parsirao i brisao, pa posle
// popravke parsera nije bilo šta ponovo obraditi i nije bilo revizorskog
// traga. Dizajn: `apps/blowing-simulator/STORAGE.md` u kube-infra repou.
//
// Ako `S3_*` promenljive nisu postavljene, store je isključen i aplikacija
// radi kao ranije (bez originala) — lokalni razvoj ne zavisi od clouda.
type SourceStore struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
	prefix  string
	enabled bool
}

const defaultSourcePrefix = "blowing/protocols/"

// NewSourceStoreFromEnv pravi store iz okruženja.
func NewSourceStoreFromEnv() *SourceStore {
	endpoint := strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	bucket := strings.TrimSpace(os.Getenv("S3_BUCKET"))
	accessKey := strings.TrimSpace(os.Getenv("S3_ACCESS_KEY"))
	secretKey := strings.TrimSpace(os.Getenv("S3_SECRET_KEY"))

	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		log.Println("source storage disabled: S3_ENDPOINT/S3_BUCKET/S3_ACCESS_KEY/S3_SECRET_KEY not set — originals will not be kept")
		return &SourceStore{}
	}

	region := getEnvOrDefault("S3_REGION", "eu-frankfurt-1")
	prefix := os.Getenv("S3_PREFIX")
	if prefix == "" {
		prefix = defaultSourcePrefix
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	cfg := aws.Config{
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		BaseEndpoint: aws.String(endpoint),
		// OCI Object Storage ne podržava `aws-chunked`; SDK inače zna da pošalje
		// checksum kojim se OCI ne snalazi (isti problem kao u FieldFlow-u).
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		// OCI zahteva path-style: endpoint/bucket/key
		o.UsePathStyle = true
	})

	log.Printf("source storage enabled: bucket=%s prefix=%s endpoint=%s", bucket, prefix, endpoint)

	return &SourceStore{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  bucket,
		prefix:  prefix,
		enabled: true,
	}
}

func (s *SourceStore) Enabled() bool {
	return s != nil && s.enabled
}

// sourceSHA256 vraća hex SHA-256 sadržaja fajla.
func sourceSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// objectKey vraća ključ objekta za dati hash.
//
// Prefiks od dva znaka sprečava da jedan „folder" ima stotine hiljada
// objekata; ime je hash, pa nema kolizija ni PII u putanji.
func (s *SourceStore) objectKey(sha string) string {
	if len(sha) < 2 {
		return s.prefix + sha + ".pdf"
	}
	return s.prefix + sha[:2] + "/" + sha + ".pdf"
}

// PutIfAbsent uploaduje PDF ako već ne postoji isti sadržaj.
func (s *SourceStore) PutIfAbsent(ctx context.Context, sha string, data []byte) error {
	if !s.Enabled() {
		return nil
	}

	key := s.objectKey(sha)

	// Ako objekat postoji, sadržaj je isti po definiciji (ime je hash).
	if _, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/pdf"),
	}); err != nil {
		return fmt.Errorf("upload originala nije uspeo: %w", err)
	}

	return nil
}

// PresignGet pravi privremeni URL za preuzimanje originala.
func (s *SourceStore) PresignGet(ctx context.Context, sha, filename string, ttl time.Duration) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("source storage nije konfigurisan")
	}

	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(sha)),
	}
	if filename != "" {
		// Da browser sačuva fajl pod originalnim imenom.
		input.ResponseContentDisposition = aws.String(
			fmt.Sprintf("attachment; filename=%q", sanitizeHeaderFilename(filename)))
	}

	out, err := s.presign.PresignGetObject(ctx, input, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign nije uspeo: %w", err)
	}
	return out.URL, nil
}

// sanitizeHeaderFilename uklanja ono što bi pokvarilo Content-Disposition.
func sanitizeHeaderFilename(name string) string {
	name = strings.ReplaceAll(name, "\"", "")
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	return name
}

// ensureSourceTables dodaje `source_objects` i kolone na `protocols`.
func ensureSourceTables(db *sqlx.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS source_objects (
			sha256        CHAR(64) PRIMARY KEY,
			object_key    TEXT NOT NULL,
			size_bytes    BIGINT NOT NULL,
			content_type  TEXT NOT NULL DEFAULT 'application/pdf',
			first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			ref_count     INTEGER NOT NULL DEFAULT 0
		)`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS source_sha256 CHAR(64)`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS source_size BIGINT`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS source_uploaded_at TIMESTAMPTZ`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("source schema: %w", err)
		}
	}
	return nil
}

// attachSource povezuje sačuvani protokol sa originalom u S3.
func attachSource(db *sqlx.DB, protocolID int, sha string, size int64, objectKey string) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO source_objects (sha256, object_key, size_bytes, ref_count)
		VALUES ($1, $2, $3, 1)
		ON CONFLICT (sha256) DO UPDATE SET ref_count = source_objects.ref_count + 1`,
		sha, objectKey, size); err != nil {
		return fmt.Errorf("source_objects: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE protocols
		SET source_sha256 = $1, source_size = $2, source_uploaded_at = NOW()
		WHERE id = $3`, sha, size, protocolID); err != nil {
		return fmt.Errorf("protocols.source_sha256: %w", err)
	}

	return tx.Commit()
}

// storeOriginal uploaduje original i vraća njegov hash.
// Vraća prazan string kad je storage isključen (feature off).
func storeOriginal(ctx context.Context, store *SourceStore, data []byte) (string, error) {
	if !store.Enabled() {
		return "", nil
	}
	sha := sourceSHA256(data)
	if err := store.PutIfAbsent(ctx, sha, data); err != nil {
		return "", err
	}
	return sha, nil
}

// sourceStore je jedan store za ceo proces. `Enabled()` je nil-safe, pa
// aplikacija radi i kad S3 nije konfigurisan.
var sourceStore *SourceStore

// prepareOriginal uploaduje original i vraća hash i veličinu.
// Kad je storage isključen, vraća prazan hash i nil grešku.
func prepareOriginal(ctx context.Context, data []byte) (string, int64, error) {
	if !sourceStore.Enabled() {
		return "", 0, nil
	}
	sha, err := storeOriginal(ctx, sourceStore, data)
	return sha, int64(len(data)), err
}

// linkOriginal povezuje sačuvani protokol sa originalom.
// Greška se loguje, ali ne obara zahtev — protokol je već u bazi.
func linkOriginal(protocolID int, sha string, size int64) {
	if sha == "" || protocolID <= 0 {
		return
	}
	if err := attachSource(db, protocolID, sha, size, sourceStore.objectKey(sha)); err != nil {
		log.Printf("source storage: vezivanje originala za protokol %d nije uspelo: %v", protocolID, err)
	}
}
