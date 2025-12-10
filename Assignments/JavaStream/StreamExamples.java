import java.util.*;
import java.util.stream.*;

public class StreamExamples {

    public static void main(String[] args) {
        filterExample();
        mapExample();
        reduceExample();
        objectStreamExample();
    }

    // filter is to keep only even number
    public static void filterExample() {
        List<Integer> nums = Arrays.asList(1, 2, 3, 4, 5, 6);

        List<Integer> evens = nums.stream()
                .filter(n -> n % 2 == 0)
                .collect(Collectors.toList());

        System.out.println("Filter Example (Even Numbers): " + evens);
    }

    // MAP — square each number
    public static void mapExample() {
        List<Integer> nums = Arrays.asList(1, 2, 3, 4, 5);

        List<Integer> squares = nums.stream()
                .map(n -> n * n)
                .collect(Collectors.toList());

        System.out.println("Map Example (Squares): " + squares);
    }

    // reduce — sum all numbers
    // -------------------------------------------------------
    public static void reduceExample() {
        List<Integer> nums = Arrays.asList(3, 7, 2, 10);
        int sum = nums.stream()
                .reduce(0, Integer::sum);
        System.out.println("Reduce Example (Sum): " + sum);
    }

    // stream with objects Filter adults and return their names
    public static void objectStreamExample() {
        List<Person> people = Arrays.asList(
                new Person("Alice", 23),
                new Person("Bob", 17),
                new Person("Charlie", 30));

        List<String> adultNames = people.stream()
                .filter(p -> p.age >= 18)
                .map(p -> p.name)
                .collect(Collectors.toList());

        System.out.println("Object Stream Example (Adult Names): " + adultNames);
    }

    // Simple Person class for example 4
    static class Person {
        String name;
        int age;

        Person(String n, int a) {
            name = n;
            age = a;
        }
    }

}
