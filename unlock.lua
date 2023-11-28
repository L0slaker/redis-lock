local currentValue = redis.call("GET",KEYS[1])

if currentValue == ARGV[1] then
    return redis.call("DEL",KEYS[1])
else
    return 0
end