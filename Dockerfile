# Container image for the shared agent terminal (cmd/agentterm).
#
#   docker run --rm -p 8787:8787 -v agentterm-data:/data \
#     ghcr.io/deontewattsv1/collaborate.asciidoc:main
#
# The server listens on 0.0.0.0:8787 inside the container. Put TLS in front
# and set AGENTTERM_READ_TOKEN before exposing it beyond localhost.
FROM golang:1.27.4-bookworm AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/agentterm ./cmd/agentterm \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/agentterm /usr/local/bin/agentterm
COPY .github/agent-registry.json /etc/agentterm/agent-registry.json
# The server runs as nonroot (65532), which must own its data directory.
COPY --from=build --chown=65532:65532 /out/data /data
VOLUME ["/data"]
EXPOSE 8787
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/agentterm"]
CMD ["serve", "--addr", "0.0.0.0:8787", "--registry", "/etc/agentterm/agent-registry.json", "--data", "/data"]
