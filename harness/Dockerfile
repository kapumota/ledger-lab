FROM golang:1.22-alpine AS build
WORKDIR /src
COPY harness/go.mod harness/go.sum ./
RUN go mod download
COPY harness/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/verifier ./cmd/verifier && \
    CGO_ENABLED=0 go build -trimpath -o /out/workload ./cmd/workload && \
    CGO_ENABLED=0 go build -trimpath -o /out/injector ./cmd/injector

FROM alpine:3.20
COPY --from=build /out/ /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/verifier"]
