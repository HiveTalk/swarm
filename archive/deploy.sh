#!/bin/bash

# Deploy script for HiveTalk Swarm with timeout fixes
# Usage: ./deploy.sh

set -e

SERVER="hivetalk.org"
REMOTE_PATH="/opt/swarm"  # Adjust this path as needed
SERVICE_NAME="swarm"      # Adjust service name as needed

echo "🚀 Deploying HiveTalk Swarm with timeout fixes..."

# Build the application locally
echo "📦 Building application..."
go build -o swarm main.go

# Copy files to remote server
echo "📤 Copying files to remote server..."
scp swarm $SERVER:$REMOTE_PATH/
scp .env $SERVER:$REMOTE_PATH/
scp nginx-config-update.conf $SERVER:/tmp/

# Deploy on remote server
echo "🔧 Updating remote server..."
ssh $SERVER << 'EOF'
    # Stop the service
    sudo systemctl stop swarm || echo "Service not running"
    
    # Make binary executable
    chmod +x /opt/swarm/swarm
    
    # Update nginx configuration
    sudo cp /tmp/nginx-config-update.conf /etc/nginx/sites-available/swarm.hivetalk.org
    
    # Create nginx temp directory
    sudo mkdir -p /tmp/nginx_uploads
    sudo chown www-data:www-data /tmp/nginx_uploads
    
    # Test and reload nginx
    sudo nginx -t
    sudo systemctl reload nginx
    
    # Start the service
    sudo systemctl start swarm
    sudo systemctl status swarm --no-pager
    
    echo "✅ Deployment complete!"
EOF

echo "🎉 Deployment finished! The server should now handle 100MB+ uploads."
