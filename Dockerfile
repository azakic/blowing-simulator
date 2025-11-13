# Dockerfile for blowing-simulator Go app with PostgreSQL
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN go build -o blowing-simulator ./cmd/blowing-simulator

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/blowing-simulator .
COPY migrations/ ./migrations/
COPY schema/ ./schema/
ENV PORT=8080
EXPOSE 8080
CMD ["./blowing-simulator"]
