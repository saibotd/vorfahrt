FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /vorfahrt .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
COPY --from=builder /vorfahrt /usr/local/bin/vorfahrt

# Setup data directory
RUN mkdir -p /data
WORKDIR /app
ENV DATA_DIR=/data
ENV ACME_FILE=/data/acme.json

# Expose SMTP and Web
EXPOSE 25 8080

ENTRYPOINT ["vorfahrt"]
