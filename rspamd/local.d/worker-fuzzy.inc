bind_socket = "*:11335";
count = 1;
backend = "redis";
expire = 180d;

allow_update = ["127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"];
