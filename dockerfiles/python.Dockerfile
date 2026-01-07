ARG BASE_IMAGE=claude-container:base
FROM ${BASE_IMAGE}

USER root
# Install Python and build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
  python3 \
  python3-pip \
  python3-venv \
  build-essential \
  libssl-dev \
  libffi-dev \
  libxml2-dev \
  libxslt1-dev \
  zlib1g-dev \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Poetry
RUN pip3 install --no-cache-dir poetry --break-system-packages

# Install uv (modern Python package manager)
RUN pip3 install --no-cache-dir uv --break-system-packages

# Set Python unbuffered mode
ENV PYTHONUNBUFFERED=1

# Create symlink for python command
RUN ln -sf /usr/bin/python3 /usr/bin/python

USER node

# Default command
CMD ["/bin/zsh", "-c", "claude"]
