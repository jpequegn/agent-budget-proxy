FROM golang:1.25.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /abp ./cmd/abp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /abp /abp
WORKDIR /tmp
ENTRYPOINT ["/abp"]
CMD ["demo", "--out", "/tmp/demo"]
