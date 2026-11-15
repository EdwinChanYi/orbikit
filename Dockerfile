# syntax=docker/dockerfile:1
# Build any service with: docker build --build-arg SERVICE=gateway -t orbikit-gateway .

FROM golang:1.27.4-alpine AS build
ARG SERVICE
ARG GOPROXY=https://proxy.golang.org,direct
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN if [ "$SERVICE" = "admin" ]; then \
      apk add --no-cache nodejs npm && \
      npm ci --prefix web/admin && \
      npm run build --prefix web/admin; \
    fi
RUN test -n "$SERVICE" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${SERVICE}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 orbikit
USER orbikit
WORKDIR /app
COPY --from=build /out/app /app/app
ENTRYPOINT ["/app/app"]
