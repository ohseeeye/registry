FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY mem ./mem
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /registry ./mem
RUN mkdir -p /runtime/tmp && chmod 1777 /runtime/tmp

FROM scratch
COPY --from=build /registry /registry
COPY --from=build /runtime/ /
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
USER 65532:65532
EXPOSE 5000
STOPSIGNAL SIGTERM
ENTRYPOINT ["/registry"]
CMD ["-listen", "0.0.0.0:5000"]
