# Stage 1: Build Frontend (Node.js)
FROM node:20-alpine AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm install
COPY frontend/ ./
RUN npm run build

# Stage 2: Build Backend (Go)
FROM golang:1.22-alpine AS backend-builder
WORKDIR /app/backend
RUN apk add --no-cache gcc musl-dev libpcap-dev
COPY backend/go.mod ./
# Pre-fetch the exact module versions listed in go.mod into the layer cache.
# -mod=mod writes go.sum without upgrading any dependency — safe with Go 1.22.
RUN GOFLAGS=-mod=mod go mod download
COPY backend/ ./
# Build. go.sum is absent from the repo; -mod=mod regenerates it on the fly
# using the cached modules (fast) while respecting the pinned versions.
RUN CGO_ENABLED=1 GOOS=linux GOFLAGS=-mod=mod \
    go build -a -installsuffix cgo -o fluxio-backend ./cmd/server/main.go

# Stage 3: Final Image
FROM alpine:latest
RUN apk --no-cache add ca-certificates libpcap tzdata
WORKDIR /root/

# Copy Go binary
COPY --from=backend-builder /app/backend/fluxio-backend .
# Copy compiled frontend dist folder into public/ directory
COPY --from=frontend-builder /app/frontend/dist ./public

EXPOSE 8080 2055/udp 37008/udp

CMD ["./fluxio-backend"]
