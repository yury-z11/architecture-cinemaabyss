FROM golang:1.23-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod ./

# Download all dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o movies-service .

# Use a smaller image for the final application
FROM alpine:latest  

#RUN apk --no-cache add ca-certificates
RUN apk --no-cache add ca-certificates postgresql-client

WORKDIR /root/

# Copy the binary from builder
COPY --from=builder /app/movies-service .

# Expose the application port
EXPOSE 8081

# Command to run the executable
#CMD ["./movies-service"]

# Ждём Postgres, потом стартуем movies-service
CMD sh -lc 'until pg_isready -h postgres -p 5432 -U postgres -d cinemaabyss; do echo "waiting for postgres..."; sleep 2; done; ./movies-service'