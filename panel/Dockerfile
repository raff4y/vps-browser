FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/panel . \
 && mkdir -p /out/data /out/policies /out/control

# Tiny image with no shell; runs as an unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/panel /panel
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/policies /policies
COPY --from=build --chown=65532:65532 /out/control /control
EXPOSE 8080 8081
ENTRYPOINT ["/panel"]
CMD ["serve"]
