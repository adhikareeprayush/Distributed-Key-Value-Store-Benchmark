FROM golang:1.23-alpine AS build
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /node ./cmd/node

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /node /usr/local/bin/node
ENTRYPOINT ["node"]
