FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /goshrt ./cmd/goshrt

FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata wget \
    && addgroup -S app \
    && adduser -S -G app app

COPY --from=builder /goshrt /usr/local/bin/goshrt

USER app

EXPOSE 8080
CMD ["goshrt"]
