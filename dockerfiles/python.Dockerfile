ARG BASE_IMAGE=claude-container:base
FROM ${BASE_IMAGE}

# Install Python and build dependencies
RUN apk add --no-cache \
    python3 \
    py3-pip \
    python3-dev \
    gcc \
    musl-dev \
    && rm -rf /var/cache/apk/*

# Install Poetry
RUN pip3 install --no-cache-dir poetry --break-system-packages

# Install uv (modern Python package manager)
RUN pip3 install --no-cache-dir uv --break-system-packages

# Set Python unbuffered mode
ENV PYTHONUNBUFFERED=1

# Create symlink for python command
RUN ln -sf /usr/bin/python3 /usr/bin/python

# Default command
CMD ["/bin/bash"]
