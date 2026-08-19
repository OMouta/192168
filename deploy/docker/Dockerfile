# Build stage
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY protocol/ ./protocol/
COPY server/ ./server/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/192168-server ./server/cmd/192168-server

# Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/192168-server /192168-server
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/192168-server"]
