FROM node:22.23.1-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.3-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/falsepoker ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=backend /out/falsepoker /falsepoker
COPY --from=frontend /src/web/dist /web
ENV PORT=8080
ENV WEB_DIR=/web
EXPOSE 8080
ENTRYPOINT ["/falsepoker"]
