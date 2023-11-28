local currentValue = redis.call("GET",KEYS[1])

if currentValue == ARGV[1] then
    return redis.call("pexpire",KEYS[1],ARGV[2])
else
    return 0
end