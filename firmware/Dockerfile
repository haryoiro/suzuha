FROM espressif/idf:v5.5.3

ARG TARGET=esp32p4

WORKDIR /firmware

SHELL ["/bin/bash", "-c"]
ENV IDF_TOOLS_EXPORT_CMD=:
ENV IDF_TOOLS_INSTALL_CMD=:

# Copy project files
COPY CMakeLists.txt ./
COPY partitions_esp32*.csv ./
COPY main/ main/
COPY components/ components/

# Copy target-specific sdkconfig + secrets (if exists)
COPY sdkconfig.defaults.${TARGET} sdkconfig.defaults
COPY sdkconfig.secret[s] ./
RUN if [ -f sdkconfig.secrets ]; then cat sdkconfig.secrets >> sdkconfig.defaults; fi

# Build
RUN source $IDF_PATH/export.sh && \
    idf.py set-target ${TARGET} && \
    idf.py build
