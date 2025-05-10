# Use a lightweight Nginx image
FROM nginx:alpine

# Copy static frontend files to the Nginx HTML directory
COPY ./index.html /usr/share/nginx/html/
COPY ./audit_log.html /usr/share/nginx/html/
COPY ./style.css /usr/share/nginx/html/
COPY ./app.js /usr/share/nginx/html/
COPY ./audit_log.js /usr/share/nginx/html/

# Copy custom Nginx configuration
COPY ./nginx.conf /etc/nginx/nginx.conf

# Expose port 80 for the web server
EXPOSE 80

# Default command to start Nginx (already handled by the base image)
# CMD ["nginx", "-g", "daemon off;"]