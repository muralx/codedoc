package com.fixture.store;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.api.Test;

class MemStoreTest {
    @Test
    void putThenGet() {
        MemStore s = new MemStore();
        s.put("a", "1");
        assertEquals("1", s.get("a"));
    }

    void helper() {}
}
