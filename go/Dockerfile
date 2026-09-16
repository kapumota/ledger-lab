FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go/go.mod ./
RUN go mod download && go mod tidy
COPY go/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/ledgerd ./cmd/ledgerd

FROM alpine:3.20
RUN adduser -D -u 10001 ledger
USER ledger
COPY --from=build /out/ledgerd /usr/local/bin/ledgerd
ENTRYPOINT ["/usr/local/bin/ledgerd"]
