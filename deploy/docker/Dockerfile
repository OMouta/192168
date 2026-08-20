# Build stage
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY protocol/ ./protocol/
COPY server/ ./server/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/192168-server ./server/cmd/192168-server

# The database directory has to exist in the image and be owned by the user the
# server runs as. Docker initializes a fresh named volume from whatever is at
# the mount path, ownership included, and a directory that only appears when the
# volume is mounted comes out owned by root, which nonroot cannot write to.
RUN mkdir /data

# Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/192168-server /192168-server
COPY --from=build --chown=nonroot:nonroot /data /data
# The default database path is relative, so it resolves against the working
# directory. Without this it lands in /, which nonroot cannot write to, and the
# server exits before it serves anything. Pointing the working directory at the
# one writable place means a deployment with no storage configured still runs.
WORKDIR /data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/192168-server"]
