# syntax=docker/dockerfile:1
FROM golang:1.25.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/beacon-vp ./cmd/beacon-vp

FROM alpine:3.21
RUN adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/beacon-vp /app/beacon-vp
USER app
ENTRYPOINT ["/app/beacon-vp"]
