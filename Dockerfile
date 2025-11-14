# Dockerfile for blowing-simulator Go app with PostgreSQL
# Use Go 1.24+ to match your local environment
FROM golang:1.24-alpine

# Install additional packages needed for the app
RUN apk --no-cache add ca-certificates tzdata poppler-utils

# Set working directory
WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application for the native architecture
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o blowing-simulator ./cmd/blowing-simulator

# Ensure the binary is executable
RUN chmod +x blowing-simulator

# Debug: Check if binary exists  
RUN ls -la blowing-simulator

# Set environment
ENV PORT=8080

# Expose port
EXPOSE 8080

# Run the application
CMD ["./blowing-simulator"]
