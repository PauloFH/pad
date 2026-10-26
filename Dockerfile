FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /pad . && mkdir /data

FROM scratch
COPY --from=build /pad /pad
# o volume nomeado herda esse dono, então o processo sem root consegue gravar
COPY --from=build --chown=65534:65534 /data /data
ENV PAD_DATA=/data PAD_ADDR=:8080
USER 65534:65534
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/pad"]
