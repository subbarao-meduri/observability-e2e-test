WORKDIR=`pwd`
cd ${WORKDIR}/..
git clone https://github.com/open-cluster-management/observability-kind-cluster.git
cd observability-kind-cluster
./setup.sh
if [ $? -ne 0 ]; then
    echo "Cannot setup environment successfully."
    exit 1
fi

# run test cases
cd ${WORKDIR}
./tests.sh
if [ $? -ne 0 ]; then
    echo "Cannot pass all test cases."
    cat ${WORKDIR}/pkg/tests/results.xml
    exit 1
fi