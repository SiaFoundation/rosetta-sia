build:
	docker build -t rosetta-sia:latest .
run:
	docker run --rm -v "${PWD}/sia-data:/data" -p 8080:8080 -p 9381:9381 rosetta-sia:latest
