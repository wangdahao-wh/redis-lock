if redis.call("get",KEYS[1]) == ARGV[1] 
then
    -- 如果当前锁的持有者与请求者一致，则续期
    return redis.call("pexpire", KEYS[1], ARGV[2])
else 
    -- 如果当前锁的持有者与请求者不一致，则尝试获取锁并设置过期时间
    return redis.call("set", KEYS[1], ARGV[1], "nx", "px", ARGV[2])
end