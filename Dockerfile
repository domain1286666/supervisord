FROM node:24-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm test && npm run build

FROM golang:1.27.2-alpine AS builder
RUN apk add --no-cache ca-certificates

COPY . /src
WORKDIR /src
COPY --from=frontend /src/webgui ./webgui

RUN CGO_ENABLED=0 go build -a -tags release -ldflags "-s -w" -o /usr/local/bin/supervisord .

FROM scratch

COPY --from=builder /usr/local/bin/supervisord /usr/local/bin/supervisord
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
EXPOSE 8002

ENTRYPOINT ["/usr/local/bin/supervisord"]
