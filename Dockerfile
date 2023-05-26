# Use the official Go base image
FROM golang:1.19

# Set the working directory inside the container
WORKDIR /app

# Copy the Go module files to the working directory
COPY go.mod go.sum ./

# Download the Go module dependencies
RUN go mod download

# Copy the Go source code to the working directory
COPY . .

# Build the Go application
RUN go build -o main .

# Set the entrypoint command
CMD ["./main"]