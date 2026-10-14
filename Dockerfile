# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src

# Cache dependencies.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/openpay ./cmd/openpay

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/openpay /usr/local/bin/openpay
COPY migrations ./migrations

ENV OPENPAY_MIGRATIONS_DIR=/app/migrations
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/openpay"]
CMD ["serve"]
