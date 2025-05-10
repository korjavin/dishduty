# Use the official Pocketbase image
FROM ghcr.io/pocketbase/pocketbase:latest

# Copy migration files
COPY ./pb_migrations /pb_migrations

# Copy hook files
COPY ./pb_hooks /pb_hooks

# Expose the port Pocketbase runs on
EXPOSE 8090

# Set the entrypoint to Pocketbase
# The ADMIN_PASS environment variable should be set when running the container.
# Example: docker run -p 8090:8090 -e ADMIN_PASS="yoursecurepassword" your-image-name
CMD ["serve", "--http=0.0.0.0:8090"]