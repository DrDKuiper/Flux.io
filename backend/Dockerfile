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
# First tidy: downloads all modules into the layer cache (fast re-build when
# only source changes, not dependencies).
RUN go mod tidy
# Copy the rest of the source — this overwrites the working directory, which
# removes the generated go.sum (it is not committed to the repo). The second
# tidy re-generates go.sum instantly because the module cache is still warm.
COPY backend/ ./
RUN go mod tidy && \
    CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o fluxio-backend ./cmd/server/main.go

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
