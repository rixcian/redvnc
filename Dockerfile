# Stage 1: Build
FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /redvnc-wsproxy ./wsproxy/cmd

# Stage 2: Runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /redvnc-wsproxy /usr/local/bin/
EXPOSE 8080
ENTRYPOINT ["redvnc-wsproxy"]
