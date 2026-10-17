FROM golang:bookworm AS build
WORKDIR /src
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/finance-home ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/finance-home /app/finance-home
ENV ADDR=:3847
ENV DATA_DIR=/data
ENV DB_PATH=/data/finance.db
EXPOSE 3847
VOLUME ["/data"]
USER nonroot:nonroot
ENTRYPOINT ["/app/finance-home"]
