# Multi-stage build — runtime image je ~60 MB umesto ~800 MB.
#
# Bitno: aplikacija čita šablone relativnom putanjom ("web/templates/..."),
# pa radni direktorijum mora biti /app i `web/` mora biti u finalnoj slici.
# `poppler-utils` je potreban jer se PDF tekst izvlači preko `pdftotext`.

FROM golang:1.24-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-w -s" -o /out/blowing-simulator ./cmd/blowing-simulator

FROM alpine:3.22
# `tesseract` + nemački/engleski jezički paket: zaglavlje „Jettlogger"
# protokola je slika, pa se čita OCR-om (vidi `internal/simulator/ocr.go`).
RUN apk --no-cache add ca-certificates tzdata poppler-utils \
    tesseract-ocr tesseract-ocr-data-deu tesseract-ocr-data-eng \
 && addgroup -g 65534 -S app 2>/dev/null || true

WORKDIR /app
COPY --from=build /out/blowing-simulator /app/blowing-simulator
COPY web ./web

ENV PORT=8080
EXPOSE 8080

# Ne pokreći kao root; /tmp je world-writable pa obrada PDF-ova radi.
USER 65534:65534

CMD ["/app/blowing-simulator"]
