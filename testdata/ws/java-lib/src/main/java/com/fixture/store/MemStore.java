package com.fixture.store;

import java.util.HashMap;
import java.util.List;
import java.util.Map;

/** MemStore keeps values in memory. */
public class MemStore implements Store {
    private final Map<String, String> data;

    /** Creates an empty store. */
    public MemStore() {
        this.data = new HashMap<>();
    }

    /** Returns the value for key, or null. */
    @Override
    public String get(String key) {
        return data.get(key);
    }

    @Override
    public void put(String key, String value) {
        data.put(key, value);
    }

    public void put(String key, int value) {
        put(key, String.valueOf(value));
    }

    static final String KIND = "memory";

    /** Kind of entries. */
    enum Entry { PLAIN, EXPIRING }

    class Cursor<T extends Comparable<T>> {
        protected void move(List<? extends T> items, String... rest) {}
    }
}
