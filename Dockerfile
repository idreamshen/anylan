FROM node:20-alpine AS webui
WORKDIR /webui
COPY webui/package*.json ./
RUN npm ci
COPY webui/ .
RUN npm run build

FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=webui /webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -o /anylan-server ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /anylan-server /usr/local/bin/anylan-server
EXPOSE 4433
USER nobody
ENTRYPOINT ["anylan-server"]
