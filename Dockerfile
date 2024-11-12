# syntax=docker/dockerfile:1

# Build the application from source
FROM golang:1.22 AS build-stage

WORKDIR     /app
COPY --link acexy/ ./

RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux go build -o /acexy

# Create a minimal image
FROM martinbjeldbak/acestream-http-proxy:2.3 AS final-stage

COPY --link             bin/entrypoint /bin/entrypoint
COPY --from=build-stage /acexy         /acexy
EXPOSE 6878 8000
# USER acestream:acestream

ENTRYPOINT [ "/bin/entrypoint" ]
