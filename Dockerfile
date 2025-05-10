# ---- Build Stage ----
FROM golang:1.24-alpine AS builder
# (Using a specific Go version, adjust if your go.mod specifies differently, 1.21 is recent)

WORKDIR /app

# Copy go.mod and go.sum first to leverage Docker cache for dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application source code
COPY . .
# Specifically main.go if other .go files are not part of the build for this service
# COPY main.go .

# Build the Go application
# CGO_ENABLED=0 is important for static builds if using scratch or minimal alpine
# GOOS=linux ensures it's built for Linux environment
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /dishduty_app main.go

# ---- Final Stage ----
FROM alpine:latest
# FROM scratch # If you want an absolutely minimal image and your app is fully static

WORKDIR /app

# Copy the compiled application from the builder stage
COPY --from=builder /dishduty_app /app/dishduty_app

# PocketBase data directory (will be mounted as a volume)
# We don't create it here, but good to note its intended path if app expects it
# RUN mkdir -p /app/pb_data

# Expose the port the application listens on (as defined in your main.go and docker-compose)
EXPOSE 8090

# Command to run the application
# The application binary itself will handle serving PocketBase
CMD ["/app/dishduty_app", "serve", "--http=0.0.0.0:8090"]