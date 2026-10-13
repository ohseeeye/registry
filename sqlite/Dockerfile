FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY sqlite ./sqlite
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /registry ./sqlite
RUN mkdir -p /runtime/data /runtime/tmp && chown 65532:65532 /runtime/data && chmod 1777 /runtime/tmp

FROM scratch
COPY --from=build /registry /registry
COPY --from=build /runtime/ /
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
USER 65532:65532
EXPOSE 5000
VOLUME /data
STOPSIGNAL SIGTERM
ENTRYPOINT ["/registry"]
CMD ["-listen", "0.0.0.0:5000", "-data", "/data"]
